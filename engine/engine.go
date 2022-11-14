package engine

import (
	"bytes"
	"context"
	"sync"
	"text/template"

	"gopkg.in/yaml.v2"

	"github.com/go-logr/logr"
	"github.com/konveyor/analyzer-lsp/hubapi"
)

type RuleEngine interface {
	RunRules(context context.Context, rules []Rule) []hubapi.Violation
	Stop()
}

type ruleMessage struct {
	rule       Rule
	returnChan chan response
}

type response struct {
	ConditionResponse ConditionResponse `yaml:"conditionResponse"`
	Err               error             `yaml:"err"`
	Rule              Rule              `yaml:"rule"`
}

type ruleEngine struct {
	// Buffered channel where Rule Processors are watching
	ruleProcessing chan ruleMessage
	cancelFunc     context.CancelFunc
	logger         logr.Logger

	wg *sync.WaitGroup
}

func CreateRuleEngine(ctx context.Context, workers int, log logr.Logger) RuleEngine {
	// Only allow for 10 rules to be waiting in the buffer at once.
	// Adding more workers will increase the number of rules running at once.
	ruleProcessor := make(chan ruleMessage, 10)

	ctx, cancelFunc := context.WithCancel(ctx)
	wg := &sync.WaitGroup{}

	for i := 0; i < workers; i++ {
		logger := log.WithValues("workder", i)
		wg.Add(1)
		go processRuleWorker(ctx, ruleProcessor, logger, wg)
	}

	return &ruleEngine{
		ruleProcessing: ruleProcessor,
		cancelFunc:     cancelFunc,
		logger:         log,
		wg:             wg,
	}
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
			logger.V(5).Info("taking rule")
			bo, err := processRule(m.rule, logger)
			m.returnChan <- response{
				ConditionResponse: bo,
				Err:               err,
				Rule:              m.rule,
			}
		case <-ctx.Done():
			logger.V(5).Info("stopping rule worker")
			wg.Done()
			return
		}
	}
}

// This will run the rules async, fanning them out, fanning them in, and then generating the results. will block until completed.
func (r *ruleEngine) RunRules(ctx context.Context, rules []Rule) []hubapi.Violation {
	// determine if we should run

	ctx, cancelFunc := context.WithCancel(ctx)

	// Need a better name for this thing
	ret := make(chan response)

	wg := &sync.WaitGroup{}
	for _, rule := range rules {
		wg.Add(1)
		r.ruleProcessing <- ruleMessage{
			rule:       rule,
			returnChan: ret,
		}
	}
	r.logger.V(5).Info("All rules added buffer, waiting for engine to complete")

	responses := []hubapi.Violation{}
	// Handle returns
	go func() {
		for {
			select {
			case response := <-ret:
				if response.Err != nil {
					r.logger.Error(response.Err, "unable to get response", "rule", response.Rule.RuleID)
				}
				if !response.ConditionResponse.Passed {
					violation, err := r.createViolation(response.ConditionResponse, response.Rule)
					if err != nil {
						r.logger.Error(err, "unable to create violation from response")
					}
					responses = append(responses, violation)
				} else {
					// Log that rule did not pass
					r.logger.V(5).Info("rule was evaluated, and we did not find a violation", "response", response)

				}
				wg.Done()
			case <-ctx.Done():
				// At this point we should just return the function, we may want to close the wait group too.
				return
			}
		}
	}()

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
	// Cannel running go-routine
	cancelFunc()
	b, err := yaml.Marshal(responses)
	if err != nil {
		r.logger.Error(err, "unable to marshal responses")
	}
	// TODO: Here we need to process the rule reponses.
	r.logger.V(5).Info(string(b))
	return responses
}

func processRule(rule Rule, log logr.Logger) (ConditionResponse, error) {
	// Here is what a worker should run when getting a rule.
	// For now, lets not fan out the running of conditions.
	return rule.When.Evaluate(log, map[string]interface{}{})

}

func (r *ruleEngine) createViolation(conditionResponse ConditionResponse, rule Rule) (hubapi.Violation, error) {
	incidents := []hubapi.Incident{}
	for _, m := range conditionResponse.Incidents {
		links := []hubapi.Link{}
		if len(m.Links) > 0 {
			for _, l := range m.Links {
				links = append(links, hubapi.Link{
					URL:   l.URL,
					Title: l.Title,
				})
			}

		}
		// extras, err := json.Marshal(m.Extras)
		// if err != nil {
		// 	return hubapi.Violation{}, err
		// }

		templateString, err := r.createPerformString(rule.Perform, m.Extras)
		if err != nil {
			r.logger.Error(err, "unable to create template string")
		}

		incidents = append(incidents, hubapi.Incident{
			URI:           m.FileURI,
			Effort:        m.Effort,
			Message:       templateString,
			Extras:        m.Extras,
			ExternalLinks: links,
		})
	}

	return hubapi.Violation{
		RuleID:      rule.RuleID,
		Description: rule.Description,
		Category:    rule.Category,
		Incidents:   incidents,
		Extras:      []byte{},
	}, nil
}

func (r *ruleEngine) createPerformString(messageTemplate string, ctx map[string]interface{}) (string, error) {
	t, err := template.New("message").Parse(messageTemplate)
	if err != nil {
		return "", err
	}
	buf := &bytes.Buffer{}
	err = t.Execute(buf, ctx)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}
