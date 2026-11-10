// Package yamlpath maps diagnostic paths such as "cells[2].ports[0]" back
// to line/column positions in a parsed YAML document.
package yamlpath

import (
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type segment struct {
	key   string
	index int
	isIdx bool
}

var idxRe = regexp.MustCompile(`\[(\d+)\]`)

func split(path string) []segment {
	var segs []segment
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		name := part
		if i := strings.IndexByte(part, '['); i >= 0 {
			name = part[:i]
		}
		if name != "" {
			segs = append(segs, segment{key: name})
		}
		for _, m := range idxRe.FindAllStringSubmatch(part, -1) {
			n, _ := strconv.Atoi(m[1])
			segs = append(segs, segment{index: n, isIdx: true})
		}
	}
	return segs
}

// Locate returns the position of the deepest node along path. If the path
// cannot be followed completely it returns the closest ancestor.
func Locate(root *yaml.Node, path string) (line, col int) {
	if root == nil {
		return 0, 0
	}
	n := root
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for _, s := range split(path) {
		next := child(n, s)
		if next == nil {
			break
		}
		n = next
	}
	return n.Line, n.Column
}

func child(n *yaml.Node, s segment) *yaml.Node {
	for n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	switch {
	case s.isIdx && n.Kind == yaml.SequenceNode:
		if s.index < len(n.Content) {
			return n.Content[s.index]
		}
	case !s.isIdx && n.Kind == yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == s.key {
				return n.Content[i+1]
			}
		}
	}
	return nil
}

var lineRe = regexp.MustCompile(`^line (\d+): (.*)$`)

// SplitError converts a yaml.v3 decoding error into (line, message) pairs.
func SplitError(err error) []struct {
	Line int
	Msg  string
} {
	var out []struct {
		Line int
		Msg  string
	}
	var msgs []string
	if te, ok := err.(*yaml.TypeError); ok {
		msgs = te.Errors
	} else {
		msgs = []string{strings.TrimPrefix(err.Error(), "yaml: ")}
	}
	for _, m := range msgs {
		m = strings.TrimPrefix(m, "yaml: ")
		if sm := lineRe.FindStringSubmatch(m); sm != nil {
			n, _ := strconv.Atoi(sm[1])
			out = append(out, struct {
				Line int
				Msg  string
			}{n, sm[2]})
		} else {
			out = append(out, struct {
				Line int
				Msg  string
			}{0, m})
		}
	}
	return out
}
