package main

import (
	"reflect"
	"testing"
)

func TestParseFlagsSplitsAtDoubleDash(t *testing.T) {
	opts, err := parseFlags([]string{"promptdump", "-p", "hi", "--", "--model", "sonnet"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.Prompt != "hi" {
		t.Errorf("Prompt=%q", opts.Prompt)
	}
	if !reflect.DeepEqual(opts.ExtraArgs, []string{"--model", "sonnet"}) {
		t.Errorf("ExtraArgs=%v", opts.ExtraArgs)
	}
}

func TestParseFlagsDefaults(t *testing.T) {
	opts, err := parseFlags([]string{"promptdump"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.Prompt != "ping" {
		t.Errorf("default Prompt=%q want ping", opts.Prompt)
	}
	if opts.OutPath != "" {
		t.Errorf("default OutPath=%q want empty", opts.OutPath)
	}
}

func TestParseFlagsOutputAndVerbose(t *testing.T) {
	opts, err := parseFlags([]string{"promptdump", "-o", "/tmp/snap", "-v"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.OutPath != "/tmp/snap" {
		t.Errorf("OutPath=%q", opts.OutPath)
	}
	if !opts.Verbose {
		t.Errorf("Verbose=false want true")
	}
}
