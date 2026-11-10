// Package asm implements the shared assembly language: a single parser
// for every architecture, and a compiler that resolves operands and
// encodes bytecode against an architecture's vm.ISA.
package asm

import (
	"fmt"
	"strconv"
	"strings"
)

// Pos is a 1-based source position.
type Pos struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Column) }

// TokenKind classifies lexical tokens.
type TokenKind uint8

const (
	TokIdent TokenKind = iota + 1
	TokNumber
	tokComma
	tokColon
)

// Token is a lexical token with its source position.
type Token struct {
	Kind TokenKind
	Text string
	Pos  Pos
}

// Statement is one instruction with the labels that precede it.
type Statement struct {
	Labels   []Token
	Mnemonic Token
	Operands []Token
}

// File is a parsed program.
type File struct {
	Statements []Statement
	// TrailingLabels are labels after the last instruction. Execution wraps
	// from the end of a program to its start, so they refer to instruction 0.
	TrailingLabels []Token
}

// Error is an assembly diagnostic.
type Error struct {
	Pos Pos
	Msg string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Pos, e.Msg) }

// ErrorList collects assembly diagnostics in source order.
type ErrorList []*Error

func (l ErrorList) Error() string {
	parts := make([]string, len(l))
	for i, e := range l {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "\n")
}

// Err returns l as an error, or nil when empty.
func (l ErrorList) Err() error {
	if len(l) == 0 {
		return nil
	}
	return l
}

func (l *ErrorList) add(pos Pos, format string, args ...any) {
	*l = append(*l, &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// Parse parses assembly source.
//
// Grammar (per line):
//
//	line      = { label ":" } [ mnemonic [ operand { [","] operand } ] ] [ comment ]
//	comment   = ("#" | ";") any-text
//	label     = identifier
//	operand   = identifier | integer
//	integer   = ["+"|"-"] (decimal | "0x" hex | "0b" binary | "0o" octal)
func Parse(src string) (*File, ErrorList) {
	var errs ErrorList
	f := &File{}
	var pending []Token
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	for i, line := range lines {
		toks, ok := lexLine(line, i+1, &errs)
		if !ok || len(toks) == 0 {
			continue
		}
		j := 0
		for j+1 < len(toks) && toks[j].Kind == TokIdent && toks[j+1].Kind == tokColon {
			pending = append(pending, toks[j])
			j += 2
		}
		if j >= len(toks) {
			continue
		}
		if toks[j].Kind != TokIdent {
			errs.add(toks[j].Pos, "expected instruction mnemonic, found %q", toks[j].Text)
			continue
		}
		st := Statement{Labels: pending, Mnemonic: toks[j]}
		st.Mnemonic.Text = strings.ToUpper(st.Mnemonic.Text)
		pending = nil
		lastComma := false
		bad := false
		for _, t := range toks[j+1:] {
			switch t.Kind {
			case tokComma:
				if len(st.Operands) == 0 || lastComma {
					errs.add(t.Pos, "unexpected ','")
					bad = true
				}
				lastComma = true
			case tokColon:
				errs.add(t.Pos, "unexpected ':' (labels must start the line)")
				bad = true
			default:
				st.Operands = append(st.Operands, t)
				lastComma = false
			}
		}
		if lastComma && !bad {
			errs.add(toks[len(toks)-1].Pos, "trailing ','")
		}
		f.Statements = append(f.Statements, st)
	}
	f.TrailingLabels = pending
	return f, errs
}

func lexLine(line string, lineNo int, errs *ErrorList) ([]Token, bool) {
	var toks []Token
	i := 0
	for i < len(line) {
		c := line[i]
		pos := Pos{lineNo, i + 1}
		switch {
		case c == '#' || c == ';':
			return toks, true
		case c == ' ' || c == '\t':
			i++
		case c == ',':
			toks = append(toks, Token{tokComma, ",", pos})
			i++
		case c == ':':
			toks = append(toks, Token{tokColon, ":", pos})
			i++
		case isIdentStart(c):
			j := i + 1
			for j < len(line) && isIdentChar(line[j]) {
				j++
			}
			toks = append(toks, Token{TokIdent, line[i:j], pos})
			i = j
		case isDigit(c) || ((c == '-' || c == '+') && i+1 < len(line) && isDigit(line[i+1])):
			j := i + 1
			for j < len(line) && isIdentChar(line[j]) {
				j++
			}
			toks = append(toks, Token{TokNumber, line[i:j], pos})
			i = j
		default:
			errs.add(pos, "unexpected character %q", c)
			return nil, false
		}
	}
	return toks, true
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentChar(c byte) bool { return isIdentStart(c) || isDigit(c) }
func isDigit(c byte) bool     { return c >= '0' && c <= '9' }

// ParseInteger parses an integer literal. Decimal literals never use the
// octal leading-zero convention ("010" is ten). The value must fit in a
// signed 32-bit integer; out-of-range literals are errors, not wrapped.
func ParseInteger(text string) (int32, error) {
	s := text
	neg := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	base := 10
	if len(s) > 2 && s[0] == '0' {
		switch s[1] {
		case 'x', 'X':
			base, s = 16, s[2:]
		case 'b', 'B':
			base, s = 2, s[2:]
		case 'o', 'O':
			base, s = 8, s[2:]
		}
	}
	if s == "" || strings.ContainsAny(s, "+-_") {
		return 0, fmt.Errorf("invalid integer literal %q", text)
	}
	u, err := strconv.ParseUint(s, base, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return 0, fmt.Errorf("integer literal %s is outside the 32-bit range [-2147483648, 2147483647]", text)
		}
		return 0, fmt.Errorf("invalid integer literal %q", text)
	}
	if neg {
		if u > 1<<31 {
			return 0, fmt.Errorf("integer literal %s is outside the 32-bit range [-2147483648, 2147483647]", text)
		}
		return int32(-int64(u)), nil
	}
	if u > 1<<31-1 {
		return 0, fmt.Errorf("integer literal %s is outside the 32-bit range [-2147483648, 2147483647]", text)
	}
	return int32(u), nil
}
