// Package diag defines the structured diagnostics shared by puzzle and
// solution validation, compilation, the CLI, and the JSON-RPC protocol.
package diag

import (
	"fmt"
	"strings"
)

// Severity of a diagnostic.
type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
)

// Diagnostic is one validation, topology, or compilation finding.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	// Code is a stable machine-readable category such as
	// "puzzle.cell.type" or "asm.syntax".
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	// File, Line, and Column locate the finding in a YAML file.
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
	// Path is the YAML path of the offending value, e.g. "cells[2].ports[0]".
	Path string `json:"path,omitempty"`
	// Cell names the puzzle cell the finding concerns, if any.
	Cell string `json:"cell,omitempty"`
	// SourceLine and SourceColumn locate assembly diagnostics within the
	// cell's program text.
	SourceLine   int `json:"sourceLine,omitempty"`
	SourceColumn int `json:"sourceColumn,omitempty"`
}

func (d Diagnostic) String() string {
	var b strings.Builder
	if d.File != "" {
		b.WriteString(d.File)
		if d.Line > 0 {
			fmt.Fprintf(&b, ":%d", d.Line)
			if d.Column > 0 {
				fmt.Fprintf(&b, ":%d", d.Column)
			}
		}
		b.WriteString(": ")
	}
	if d.Severity == Warning {
		b.WriteString("warning: ")
	}
	if d.Path != "" {
		b.WriteString(d.Path)
		b.WriteString(": ")
	}
	if d.Cell != "" && d.SourceLine > 0 {
		fmt.Fprintf(&b, "cell %s, line %d:%d: ", d.Cell, d.SourceLine, d.SourceColumn)
	} else if d.Cell != "" && d.Path == "" {
		fmt.Fprintf(&b, "cell %s: ", d.Cell)
	}
	b.WriteString(d.Message)
	return b.String()
}

// List is an ordered collection of diagnostics. It implements error.
type List []Diagnostic

// HasErrors reports whether any diagnostic has error severity.
func (l List) HasErrors() bool {
	for _, d := range l {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

func (l List) Error() string {
	parts := make([]string, len(l))
	for i, d := range l {
		parts[i] = d.String()
	}
	return strings.Join(parts, "\n")
}

// Err returns l as an error if it contains errors, otherwise nil.
func (l List) Err() error {
	if l.HasErrors() {
		return l
	}
	return nil
}

// Errorf appends an error diagnostic at a YAML path.
func (l *List) Errorf(code, path, format string, args ...any) {
	*l = append(*l, Diagnostic{Severity: Error, Code: code, Path: path, Message: fmt.Sprintf(format, args...)})
}

// Warnf appends a warning diagnostic at a YAML path.
func (l *List) Warnf(code, path, format string, args ...any) {
	*l = append(*l, Diagnostic{Severity: Warning, Code: code, Path: path, Message: fmt.Sprintf(format, args...)})
}
