package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
)

// Inputs are existing per-invocation prompt files. Inspection never generates
// a new prompt or performs discovery, model calls or checkpoint writes.
func validInputName(name string) bool {
	return len(name) <= 240 && strings.HasSuffix(name, ".prompt.txt") && validLogName(strings.TrimSuffix(name, ".prompt.txt")+".log")
}
func ReadInputs(repo, id, name string, limit int, offset int64) (LogReport, error) {
	if offset < -1 || offset >= 0 && name == "" {
		return LogReport{}, errors.New("input offset must be nonnegative and requires a prompt filename")
	}
	return readSavedRange(repo, id, false, name, limit, offset, true)
}
func FormatInputs(r LogReport) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Saved agent inputs · %s\n%s\n", r.ID, r.Directory)
	if r.Name == "" {
		for _, f := range r.Files {
			fmt.Fprintf(&out, "%s · %d bytes\n", f.Name, f.Bytes)
		}
		if len(r.Files) == 0 {
			out.WriteString("No agent inputs saved. Simulated or undispatched calls may have no input file.\n")
		}
		out.WriteString("Read one with buildheadway inputs [--run ID] PROMPT_NAME.\n")
	} else {
		fmt.Fprintf(&out, "%s · %d bytes · offset %d\n", r.Name, r.Bytes, r.Offset)
		if r.Truncated {
			out.WriteString("[Input excerpt; --offset 0 reads from the beginning. Offsets are bytes.]\n")
		}
		out.WriteString(r.Output)
		if r.More {
			fmt.Fprintf(&out, "\n[Continue with --offset %d.]", r.NextOffset)
		}
	}
	return strings.TrimSpace(out.String())
}
func runInputsCommand(args []string) int {
	f := flag.NewFlagSet("buildheadway inputs", flag.ContinueOnError)
	repo := f.String("project", ".", "Project folder")
	id := f.String("run", "latest", "Saved goal ID")
	nameJSON := f.Bool("json", false, "Print JSON")
	limit := f.Int("bytes", 16000, "Maximum bytes (1–1048576)")
	offset := f.Int64("offset", -1, "Read from this byte offset; requires PROMPT_NAME")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if f.NArg() > 1 {
		return fail(errors.New("usage: buildheadway inputs [--run ID] [PROMPT_NAME]; options precede the filename"))
	}
	explicit := false
	f.Visit(func(v *flag.Flag) {
		if v.Name == "offset" {
			explicit = true
		}
	})
	if explicit && *offset < 0 {
		return fail(errors.New("input offset must be nonnegative"))
	}
	name := ""
	if f.NArg() == 1 {
		name = f.Arg(0)
	}
	r, err := ReadInputs(*repo, *id, name, *limit, *offset)
	if err != nil {
		return fail(err)
	}
	if err := outputValue(r, FormatInputs(r), *nameJSON); err != nil {
		return fail(err)
	}
	return 0
}
