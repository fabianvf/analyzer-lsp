package engine

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"go.lsp.dev/uri"
	"go.opentelemetry.io/otel/attribute"

	"github.com/cbroglie/mustache"
	"github.com/go-logr/logr"
	"github.com/konveyor/analyzer-lsp/hubapi"
	"github.com/konveyor/analyzer-lsp/tracing"
)

const (
	// TODO: make this configurable in the future
	// We may or may not need to do this so holding off for now.
	CONTEXT_LINES = 10
)

type RuleEngine interface {
	RunRules(context context.Context, rules []RuleSet, selectors ...RuleSelector) []hubapi.RuleSet
	Stop()
}

type ruleMessage struct {
	rule        Rule
	ruleSetName string
	ctx         ConditionContext
	returnChan  chan response
}

type response struct {
	ConditionResponse ConditionResponse `yaml:"conditionResponse"`
	Err               error             `yaml:"err"`
	Rule              Rule              `yaml:"rule"`
	RuleSetName       string
}

type ruleEngine struct {
	// Buffered channel where Rule Processors are watching
	ruleProcessing chan ruleMessage
	cancelFunc     context.CancelFunc
	logger         logr.Logger

	wg *sync.WaitGroup

	incidentLimit int
	codeSnipLimit int
}

type Option func(engine *ruleEngine)

func WithIncidentLimit(i int) Option {
	return func(engine *ruleEngine) {
		engine.incidentLimit = i
	}
}

func WithCodeSnipLimit(i int) Option {
	return func(engine *ruleEngine) {
		engine.codeSnipLimit = i
	}
}

func CreateRuleEngine(ctx context.Context, workers int, log logr.Logger, options ...Option) RuleEngine {
	// Only allow for 10 rules to be waiting in the buffer at once.
	// Adding more workers will increase the number of rules running at once.
	ruleProcessor := make(chan ruleMessage, 10)

	ctx, cancelFunc := context.WithCancel(ctx)
	wg := &sync.WaitGroup{}

	for i := 0; i < workers; i++ {
		logger := log.WithValues("worker", i)
		wg.Add(1)
		go processRuleWorker(ctx, ruleProcessor, logger, wg)
	}

	r := &ruleEngine{
		ruleProcessing: ruleProcessor,
		cancelFunc:     cancelFunc,
		logger:         log,
		wg:             wg,
	}
	for _, o := range options {
		o(r)
	}
	return r
}

func (r *ruleEngine) Stop() {
	r.cancelFunc()
	r.logger.V(5).Info("rule engine stopping")
	r.wg.Wait()
}

func processRuleWorker(ctx context.Context, ruleMessages chan ruleMessage, logger logr.Logger, wg *sync.WaitGroup) {
	for {
		select {
		case m := <-ruleMessages:
			logger.V(5).Info("taking rule", "ruleset", m.ruleSetName, "rule", m.rule.RuleID)
			m.ctx.Template = make(map[string]ChainTemplate)
			bo, err := processRule(ctx, m.rule, m.ctx, logger)
			logger.V(5).Info("finished rule", "found", len(bo.Incidents), "error", err, "rule", m.rule.RuleID)
			m.returnChan <- response{
				ConditionResponse: bo,
				Err:               err,
				Rule:              m.rule,
				RuleSetName:       m.ruleSetName,
			}
		case <-ctx.Done():
			logger.V(5).Info("stopping rule worker")
			wg.Done()
			return
		}
	}
}

func (r *ruleEngine) createRuleSet(ruleSet RuleSet) *hubapi.RuleSet {
	rs := &hubapi.RuleSet{
		Name:        ruleSet.Name,
		Description: ruleSet.Description,
		Tags:        []string{},
		Violations:  map[string]hubapi.Violation{},
		Errors:      map[string]string{},
		Unmatched:   []string{},
		Skipped:     []string{},
	}
	return rs
}

// This will run tagging rules first, synchronously, generating tags to pass on further as context to other rules
// then runs remaining rules async, fanning them out, fanning them in, finally generating the results. will block until completed.
func (r *ruleEngine) RunRules(ctx context.Context, ruleSets []RuleSet, selectors ...RuleSelector) []hubapi.RuleSet {
	// determine if we should run

	ctx, cancelFunc := context.WithCancel(ctx)

	taggingRules, otherRules, mapRuleSets := r.filterRules(ruleSets, selectors...)

	ruleContext := r.runTaggingRules(ctx, taggingRules, mapRuleSets)

	// Need a better name for this thing
	ret := make(chan response)

	wg := &sync.WaitGroup{}
	// Handle returns
	go func() {
		for {
			select {
			case response := <-ret:
				func() {
					r.logger.Info("rule returned", "rule", response.Rule.RuleID)
					defer wg.Done()
					if response.Err != nil {
						r.logger.Error(response.Err, "failed to evaluate rule", "ruleID", response.Rule.RuleID)
						if rs, ok := mapRuleSets[response.RuleSetName]; ok {
							rs.Errors[response.Rule.RuleID] = response.Err.Error()
						}
					} else if response.ConditionResponse.Matched {
						violation, err := r.createViolation(response.ConditionResponse, response.Rule)
						if err != nil {
							r.logger.Error(err, "unable to create violation from response")
						}
						rs, ok := mapRuleSets[response.RuleSetName]
						if !ok {
							r.logger.Info("this should never happen that we don't find the ruleset")
						}
						rs.Violations[response.Rule.RuleID] = violation
					} else {
						// Log that rule did not pass
						r.logger.V(5).Info("rule was evaluated, and we did not find a violation", "rule", response.Rule.RuleID)
						if rs, ok := mapRuleSets[response.RuleSetName]; ok {
							rs.Unmatched = append(rs.Unmatched, response.Rule.RuleID)
						}
					}
				}()
			case <-ctx.Done():
				// At this point we should just return the function, we may want to close the wait group too.
				return
			}
		}
	}()

	for _, rule := range otherRules {
		wg.Add(1)
		rule.returnChan = ret
		rule.ctx = ruleContext
		r.ruleProcessing <- rule
	}
	r.logger.V(5).Info("All rules added buffer, waiting for engine to complete", "size", len(otherRules))

	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()

	// Wait for all the rules to process
	select {
	case <-done:
		r.logger.V(2).Info("done processing all the rules")
	case <-ctx.Done():
		r.logger.V(1).Info("processing of rules was canceled")
	}
	responses := []hubapi.RuleSet{}
	for _, ruleSet := range mapRuleSets {
		if ruleSet != nil {
			responses = append(responses, *ruleSet)
		}
	}
	// Cannel running go-routine
	cancelFunc()
	return responses
}

// filterRules splits rules into tagging and other rules
func (r *ruleEngine) filterRules(ruleSets []RuleSet, selectors ...RuleSelector) ([]ruleMessage, []ruleMessage, map[string]*hubapi.RuleSet) {
	// filter rules that generate tags, they run first
	taggingRules := []ruleMessage{}
	mapRuleSets := map[string]*hubapi.RuleSet{}
	// all rules except meta
	otherRules := []ruleMessage{}
	for _, ruleSet := range ruleSets {
		mapRuleSets[ruleSet.Name] = r.createRuleSet(ruleSet)
		for _, rule := range ruleSet.Rules {
			// labels on ruleset apply to all rules in it
			rule.Labels = append(rule.Labels, ruleSet.Labels...)
			// skip rule when doesn't match any selector
			if !matchesAllSelectors(rule.RuleMeta, selectors...) {
				mapRuleSets[ruleSet.Name].Skipped = append(mapRuleSets[ruleSet.Name].Skipped, rule.RuleID)
				r.logger.Info("one or more selectors did not match for rule, skipping", "rule", rule.RuleMeta)
				continue
			}

			if rule.Perform.Tag == nil {
				otherRules = append(otherRules, ruleMessage{
					rule:        rule,
					ruleSetName: ruleSet.Name,
				})
			} else {
				taggingRules = append(taggingRules, ruleMessage{
					rule:        rule,
					ruleSetName: ruleSet.Name,
				})
				// if both message and tag are set
				// split message part into a new rule
				if rule.Perform.Message.Text != nil {
					rule.Perform.Tag = nil
					otherRules = append(
						otherRules,
						ruleMessage{
							rule:        rule,
							ruleSetName: ruleSet.Name,
						},
					)
				}
			}
		}
	}
	return taggingRules, otherRules, mapRuleSets
}

// runTaggingRules filters and runs info rules synchronously
// returns list of non-info rules, a context to pass to them
func (r *ruleEngine) runTaggingRules(ctx context.Context, infoRules []ruleMessage, mapRuleSets map[string]*hubapi.RuleSet) ConditionContext {
	context := ConditionContext{
		Tags:     make(map[string]interface{}),
		Template: make(map[string]ChainTemplate),
	}
	// track unique tags per ruleset
	rulesetTagsCache := map[string]map[string]bool{}
	for _, ruleMessage := range infoRules {
		rule := ruleMessage.rule
		response, err := processRule(ctx, rule, context, r.logger)
		if err != nil {
			r.logger.Error(err, "failed to evaluate rule", "ruleID", rule.RuleID)
			if rs, ok := mapRuleSets[ruleMessage.ruleSetName]; ok {
				rs.Errors[rule.RuleID] = err.Error()
			}
		} else if response.Matched {
			r.logger.V(5).Info("info rule was matched", "ruleID", rule.RuleID)
			for _, tagString := range rule.Perform.Tag {
				tags, err := parseTagsFromPerformString(tagString)
				if err != nil {
					r.logger.Error(err, "unable to create tags", "ruleID", rule.RuleID)
					continue
				}
				for _, tag := range tags {
					context.Tags[tag] = true
				}
			}
			rs, ok := mapRuleSets[ruleMessage.ruleSetName]
			if !ok {
				r.logger.Info("this should never happen that we don't find the ruleset")
			} else {
				if _, ok := rulesetTagsCache[rs.Name]; !ok {
					rulesetTagsCache[rs.Name] = make(map[string]bool)
				}
				for _, tag := range rule.Perform.Tag {
					if _, ok := rulesetTagsCache[rs.Name][tag]; !ok {
						rulesetTagsCache[rs.Name][tag] = true
						rs.Tags = append(rs.Tags, tag)
					}
				}
				mapRuleSets[ruleMessage.ruleSetName] = rs
			}
		} else {
			r.logger.Info("info rule not matched", "rule", rule.RuleID)
			if rs, ok := mapRuleSets[ruleMessage.ruleSetName]; ok {
				rs.Unmatched = append(rs.Unmatched, rule.RuleID)
			}
		}
	}
	return context
}

func parseTagsFromPerformString(tagString string) ([]string, error) {
	tags := []string{}
	pattern := regexp.MustCompile(`^(?:[\w- ]+=){0,1}([\w- ]+(?:, *[\w- ,]+)*),?$`)
	if !pattern.MatchString(tagString) {
		return nil, fmt.Errorf("unexpected tag string %s", tagString)
	}
	for _, groups := range pattern.FindAllStringSubmatch(tagString, -1) {
		for _, tag := range strings.Split(groups[1], ",") {
			if tag != "" {
				tags = append(tags, strings.Trim(tag, " "))
			}
		}
	}
	return tags, nil
}

func processRule(ctx context.Context, rule Rule, ruleCtx ConditionContext, log logr.Logger) (ConditionResponse, error) {
	ctx, span := tracing.StartNewSpan(
		ctx, "process-rule", attribute.Key("rule").String(rule.RuleID))
	defer span.End()
	// Here is what a worker should run when getting a rule.
	// For now, lets not fan out the running of conditions.
	return rule.When.Evaluate(ctx, log, ruleCtx)

}

func (r *ruleEngine) createViolation(conditionResponse ConditionResponse, rule Rule) (hubapi.Violation, error) {
	incidents := []hubapi.Incident{}
	fileCodeSnipCount := map[string]int{}
	incidentsSet := map[string]struct{}{} // Set of incidents
	for _, m := range conditionResponse.Incidents {
		// Exit loop, we don't care about any incidents past the filter.
		if r.incidentLimit != 0 && len(incidents) == r.incidentLimit {
			break
		}
		incident := hubapi.Incident{
			URI:       m.FileURI,
			Variables: m.Variables,
		}
		links := []hubapi.Link{}
		if len(m.Links) > 0 {
			for _, l := range m.Links {
				links = append(links, hubapi.Link{
					URL:   l.URL,
					Title: l.Title,
				})
			}
		}
		// Some violations may not have a location in code.
		limitSnip := (r.codeSnipLimit != 0 && fileCodeSnipCount[string(m.FileURI)] == r.codeSnipLimit)
		if m.CodeLocation != nil && strings.HasPrefix(string(m.FileURI), uri.FileScheme) && !limitSnip {
			//Find the file, open it in a buffer.
			readFile, err := os.Open(m.FileURI.Filename())
			if err != nil {
				r.logger.V(5).Error(err, "Unable to read file")
				return hubapi.Violation{}, err
			}
			defer readFile.Close()

			scanner := bufio.NewScanner(readFile)
			lineNumber := 0
			codeSnip := ""
			for scanner.Scan() {
				if (lineNumber - CONTEXT_LINES) == m.CodeLocation.EndPosition.Line {
					codeSnip = codeSnip + fmt.Sprintf("%v", scanner.Text())
					break
				}
				if (lineNumber + CONTEXT_LINES) >= m.CodeLocation.StartPosition.Line {
					codeSnip = codeSnip + fmt.Sprintf("%v", scanner.Text())
				}
				lineNumber += 1
			}
			incident.CodeSnip = codeSnip
			fileCodeSnipCount[string(m.FileURI)] += 1
		}

		if len(rule.CustomVariables) > 0 {
			for _, cv := range rule.CustomVariables {
				match := cv.Pattern.FindStringSubmatch(incident.CodeSnip)
				switch len(match) {
				case 0:
					m.Variables[cv.Name] = cv.DefaultValue
					continue
				case 1:
					m.Variables[cv.Name] = match[0]
					continue
				case 2:
					m.Variables[cv.Name] = match[1]
				default:
					// if more than 1 match, then we have to look up the names.
					found := false
					for i, n := range cv.Pattern.SubexpNames() {
						if n == cv.NameOfCaptureGroup {
							m.Variables[cv.Name] = match[i]
							found = true
							break
						}
					}
					if !found {
						m.Variables[cv.Name] = cv.DefaultValue
					}
				}
			}
		}

		if rule.Perform.Message.Text != nil {
			templateString, err := r.createPerformString(*rule.Perform.Message.Text, m.Variables)
			if err != nil {
				r.logger.Error(err, "unable to create template string")
			}
			incident.Message = templateString
		}

		lineNumber, hasLineNumber := incident.Variables["lineNumber"]
		if !hasLineNumber {
			lineNumber = -1
		}

		incidentString := fmt.Sprintf("%s-%s-%d", incident.URI, incident.Message, lineNumber) // Formating a unique string for an incident

		// Adding it to list  and set if no duplicates found
		if _, isDuplicate := incidentsSet[incidentString]; !isDuplicate {
			incidents = append(incidents, incident)
			incidentsSet[incidentString] = struct{}{}
		}
	}

	return hubapi.Violation{
		Description: rule.Description,
		Labels:      rule.Labels,
		Category:    rule.Category,
		Incidents:   incidents,
		Extras:      []byte{},
		Effort:      rule.Effort,
		Links:       rule.Perform.Message.Links,
	}, nil
}

func (r *ruleEngine) createPerformString(messageTemplate string, ctx map[string]interface{}) (string, error) {
	return mustache.Render(messageTemplate, ctx)
}

// matchesAllSelectors returns false when any one of the selectors does not match
// selectors can be of different types e.g. label-selector, or category-selector
// when multiple selectors are present, we want all of them to match to filter-in the rule.
func matchesAllSelectors(m RuleMeta, selectors ...RuleSelector) bool {
	for _, s := range selectors {
		if !s.Matches(m) {
			return false
		}
	}
	return true
}
