package engine

import (
	"testing"

	"github.com/topolink/topolink/pkg/semantic"
)

func TestPolicyRuleEnabled(t *testing.T) {
	p := Policy{DisabledRules: map[string]bool{"r8": true, "money-write": true}}
	if p.ruleEnabled("R8") {
		t.Error("R8 should be disabled")
	}
	if p.ruleEnabled("R9") { // R9 的语义名是 money-write
		t.Error("R9 should be disabled via semantic name")
	}
	if !p.ruleEnabled("R1") {
		t.Error("R1 should stay enabled")
	}
}

func TestPolicySensitiveTables(t *testing.T) {
	p := Policy{
		SensitiveTables: []string{"ledger"},
		IgnoredTables:   []string{"tmp_"},
	}
	if !p.isSensitiveTable("settlement_ledger", nil) {
		t.Error("ledger should be sensitive")
	}
	if p.isSensitiveTable("tmp_ledger", nil) {
		t.Error("tmp_ prefix should be ignored")
	}
	if p.isSensitiveTable("orders", nil) {
		t.Error("orders should not be sensitive")
	}
}

func TestPolicyExtraPublicPath(t *testing.T) {
	p := Policy{PublicPaths: map[string]bool{"webhook": true}}
	if !p.isPublicPath("/webhook/github") {
		t.Error("/webhook/github should be public")
	}
	if p.isPublicPath("/webhook-secrets") {
		t.Error("/webhook-secrets should not be public")
	}
	if !p.isPublicPath("/health") {
		t.Error("/health should remain public from built-ins")
	}
}

func TestPolicySensitiveViaAnnotation(t *testing.T) {
	p := Policy{}
	anns := map[string]semantic.Annotation{
		"table:core_records": {Text: "financial ledger"},
	}
	if !p.isSensitiveTable("core_records", anns) {
		t.Error("annotation should mark table sensitive")
	}
}
