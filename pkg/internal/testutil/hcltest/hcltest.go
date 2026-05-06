// Package hcltest is the minimal helper set tests use to assert
// against rendered HCL. Three things:
//
//   ParseValid  — parses bytes; fails the test on syntax errors.
//   FindBlock   — walks the body tree to a named block.
//   StringAttr  — pulls a literal string attribute off a block.
//
// Anything more elaborate goes inline in the test — keeping this
// surface tiny means each test reads start-to-finish.
package hcltest

import (
	"testing"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// ParseValid parses HCL bytes and fails the test if they don't parse.
// Returns the body for further structural assertions.
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

// FindBlock returns the first block matching type + labels. Returns
// nil if not found — caller decides whether absence is a failure.
//
// Pass labels to disambiguate "resource" / "backend" / "output" etc:
//
//	FindBlock(body, "terraform")              // top-level terraform { … }
//	FindBlock(body, "backend", "s3")          // backend "s3" { … }
//	FindBlock(body, "resource", "hcloud_server", "master")
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

// StringAttr returns the literal string value of a top-level attribute
// on the block. Returns "" + ok=false if the attribute is missing or
// not a literal string (e.g. a reference like hcloud_network.default.id —
// references resolve to non-string ctys).
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
