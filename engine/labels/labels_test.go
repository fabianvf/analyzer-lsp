package labels

import (
	"testing"

	"github.com/konveyor/analyzer-lsp/engine"
)

func Test_getBooleanExpression(t *testing.T) {
	tests := []struct {
		name          string
		expr          string
		compareLabels map[string][]string
		want          string
	}{
		{
			name: "complex expression 001",
			expr: "val && (konveyor.io/k1=20 && !konveyor.io/k2=30)",
			compareLabels: map[string][]string{
				"konveyor.io/k1": {"20"},
			},
			want: "false && ( true && ! false )",
		},
		{
			name: "complex expression 002",
			expr: "val && (konveyor.io/k1=20 && !konveyor.io/k2=30) || !val2",
			compareLabels: map[string][]string{
				"konveyor.io/k1": {"20"},
				"konveyor.io/k2": {"40"},
				"val2":           {""},
				"val":            {""},
			},
			want: "true && ( true && ! false ) || ! true",
		},
		{
			name: "complex expression 003",
			expr: "val && ((((((konveyor.io/k2=40)))))) || !val2",
			compareLabels: map[string][]string{
				"konveyor.io/k1": {"20"},
				"konveyor.io/k2": {"40"},
				"val2":           {""},
				"val":            {""},
			},
			want: "true && ( ( ( ( ( ( true ) ) ) ) ) ) || ! true",
		},
		{
			name: "duplicate keys 001",
			expr: "val && (konveyor.io/k2=40 || konveyor.io/k2=20)",
			compareLabels: map[string][]string{
				"konveyor.io/k1": {"20"},
				"konveyor.io/k2": {"40"},
				"val2":           {""},
				"val":            {""},
			},
			want: "true && ( true || false )",
		},
		{
			name: "duplicate keys 002",
			expr: "konveyor.io/k1=40 || (konveyor.io/k2=30 && konveyor.io/k2=40)",
			compareLabels: map[string][]string{
				"konveyor.io/k1": {"20"},
				"konveyor.io/k2": {"40", "30"},
			},
			want: "false || ( true && true )",
		},
		{
			name: "duplicate keys 003",
			expr: "(konveyor.io/k1=40 || konveyor.io/k1=10) || (konveyor.io/k2=30 && konveyor.io/k2=40)",
			compareLabels: map[string][]string{
				"konveyor.io/k1": {"20"},
				"konveyor.io/k2": {"40", "30"},
			},
			want: "( false || false ) || ( true && true )",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getBooleanExpression(tt.expr, tt.compareLabels); got != tt.want {
				t.Errorf("getBooleanExpression() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseLabel(t *testing.T) {
	tests := []struct {
		name    string
		label   string
		wantKey string
		wantVal string
		wantErr bool
	}{
		{
			name:    "valid label 001",
			label:   "valid-label=",
			wantKey: "valid-label",
			wantVal: "",
		},
		{
			name:    "valid label 002",
			label:   "konveyor.io/valid-label",
			wantKey: "konveyor.io/valid-label",
			wantVal: "",
		},
		{
			name:    "valid label 003",
			label:   "dev.konveyor.io/sourceTech=tech10",
			wantKey: "dev.konveyor.io/sourceTech",
			wantVal: "tech10",
		},
		{
			name:    "valid label 004",
			label:   "dev.konveyor.io/sourceTech=",
			wantKey: "dev.konveyor.io/sourceTech",
			wantVal: "",
		},
		{
			name:    "invalid label 001",
			label:   "dev.konveyor#/sourceTech=tech10",
			wantErr: true,
		},
		{
			name:    "invalid label 002",
			label:   "dev.konveyor./sourceTech=tech10",
			wantErr: true,
		},
		{
			name:    "invalid label 003",
			label:   "dev.konveyor/",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKey, gotVal, err := ParseLabel(tt.label)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRuleLabel() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotKey != tt.wantKey {
				t.Errorf("ParseRuleLabel() gotKey = %v, wantKey %v", gotKey, tt.wantKey)
			}
			if gotVal != tt.wantVal {
				t.Errorf("ParseRuleLabel() gotVal = %v, wantVal %v", gotVal, tt.wantVal)
			}
		})
	}
}

func TestNewRuleSelector(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		wantErr bool
	}{
		{
			name:    "valid expression 001",
			expr:    "prefix-less-value=",
			wantErr: false,
		},
		{
			name:    "valid expression 002",
			expr:    "((konveyor.io/sourceTech=eap7 && konveyor.io/targetTech=eap8) || !another-prefixless-val)",
			wantErr: false,
		},
		{
			name:    "valid expression 003",
			expr:    "!konveyor.io/sourceTech=golang && konveyor.io/targetTech=eap89 || ((a) && (a || konveyor.io/tech=30))",
			wantErr: false,
		},
		{
			name:    "valid expression 004",
			expr:    "!((((!(k1=v1) && (k2=v2) || konveyor.io/targetTech=java) && val || !val) && k2=v2) || !v3)",
			wantErr: false,
		},
		{
			name:    "invalid expression 001",
			expr:    "&&",
			wantErr: true,
		},
		{
			name:    "invalid expression 002",
			expr:    "a &&",
			wantErr: true,
		},
		{
			name:    "invalid expression 003",
			expr:    "k1=v1 || k2$$",
			wantErr: true,
		},
		{
			name:    "duplicate keys 001",
			expr:    "konveyor.io/source=go && konveyor.io/source=java",
			wantErr: false,
		},
		{
			name: "duplicate keys 002",
			expr: "(konveyor.io/source=java && konveyor.io/source=go) || (konveyor.io/target=java && konveyor.io/target=java)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRuleSelector(tt.expr)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewRuleSelector() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func Test_ruleSelector_Matches(t *testing.T) {
	tests := []struct {
		name       string
		expr       string
		ruleLabels []string
		want       bool
	}{
		{
			name: "simple && query",
			expr: "konveyor.io/sourceTech=eap7 && konveyor.io/targetTech=eap10",
			ruleLabels: []string{
				"konveyor.io/sourceTech=eap7",
				"konveyor.io/targetTech=eap10",
			},
			want: true,
		},
		{
			name: "simple && query with !, no match",
			expr: "konveyor.io/sourceTech=eap7 && !konveyor.io/targetTech=eap10",
			ruleLabels: []string{
				"konveyor.io/sourceTech=eap7",
				"konveyor.io/targetTech=eap10",
			},
			want: false,
		},
		{
			name: "simple && query with !, match",
			expr: "konveyor.io/sourceTech=eap7 && !konveyor.io/targetTech=eap10",
			ruleLabels: []string{
				"konveyor.io/sourceTech=eap7",
				"konveyor.io/targetTech=eap11",
			},
			want: true,
		},
		{
			name: "nested && and || queries with !, match",
			expr: "(konveyor.io/sourceTech=eap7 && konveyor.io/targetTech=eap10) || (special-rule && !konveyor.io/type=restricted)",
			ruleLabels: []string{
				"konveyor.io/sourceTech=eap7",
				"konveyor.io/targetTech=eap11",
				"special-rule",
				"konveyor.io/type=special",
			},
			want: true,
		},
		{
			name: "nested && and || queries with mixed dots",
			expr: "(konveyor.io/sourceTech=eap7 && konveyor.io/targetTech=eap10) || (special-rule && !konveyor.io/type=restricted)",
			ruleLabels: []string{
				"konveyor.io/sourceTech=eap7",
				"konveyor.io/targetTech=eap11",
				"special-rule",
				"konveyor.io/type=special",
			},
			want: true,
		},
		{
			name: "nested && and || queries with mixed dots, unmatched",
			expr: "(konveyor.io/sourceTech=eap7 && konveyor.io/targetTech=eap10) || (!special-rule && !konveyor.io/type=restricted)",
			ruleLabels: []string{
				"konveyor.io/sourceTech=eap8",
				"konveyor.io/targetTech=eap11",
				"special-rule=diff-val",
				"konveyor.io/type=special",
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := NewRuleSelector(tt.expr)
			if got := s.Matches(engine.RuleMeta{Labels: tt.ruleLabels}); got != tt.want {
				t.Errorf("ruleSelector.Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}
