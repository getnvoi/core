// Package hcltest is the minimal helper set tests use to assert
// against rendered HCL. Three things:
//
//	ParseValid  — parses bytes; fails the test on syntax errors.
//	FindBlock   — walks the body tree to a named block.
//	StringAttr  — pulls a literal string attribute off a block.
package hcltest

import (
	"testing"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

func ParseValid(t *testing.T, src []byte, filename string) *hclsyntax.Body {
	t.Helper()
	f, diags := hclparse.NewParser().ParseHCL(src, filename)
	if diags.HasErrors() {
		t.Fatalf("invalid HCL in %s:\n%s\n--- source ---\n%s", filename, diags, src)
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		t.Fatalf("%s: body is not *hclsyntax.Body (got %T)", filename, f.Body)
	}
	return body
}

func FindBlock(body *hclsyntax.Body, blockType string, labels ...string) *hclsyntax.Block {
	for _, b := range body.Blocks {
		if b.Type != blockType {
			continue
		}
		if len(b.Labels) != len(labels) {
			continue
		}
		match := true
		for i, want := range labels {
			if b.Labels[i] != want {
				match = false
				break
			}
		}
		if match {
			return b
		}
	}
	return nil
}

func StringAttr(block *hclsyntax.Block, name string) (string, bool) {
	if block == nil {
		return "", false
	}
	attr, ok := block.Body.Attributes[name]
	if !ok {
		return "", false
	}
	val, diags := attr.Expr.Value(nil)
	if diags.HasErrors() || val.Type().FriendlyName() != "string" {
		return "", false
	}
	return val.AsString(), true
}
