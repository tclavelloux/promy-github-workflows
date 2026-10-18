package syncheck

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A deliberately small TOML reader: enough to read pyproject.toml and
// osv-scanner.toml without a new dependency. It parses the whole TOML 1.0
// syntax that can appear in those files (tables, arrays of tables, dotted and
// quoted keys, every string form, arrays, inline tables, numbers, booleans,
// dates) and keeps only what the rules need: scalar values by full key path,
// plus the entries of each top-level array of tables. A construct it does not
// understand is a parse error, never a silent pass.

type tomlKind int

const (
	tomlString tomlKind = iota
	tomlInt
	tomlFloat
	tomlBool
	tomlDate // offset/local date-time or local date
	tomlArray
)

func (k tomlKind) String() string {
	return [...]string{"string", "integer", "float", "boolean", "date", "array"}[k]
}

type tomlValue struct {
	kind tomlKind
	str  string // decoded string, else the raw literal
	line int
}

// tomlTable is one [[name]] entry; its keys are relative to the entry.
type tomlTable struct {
	line   int
	values map[string]tomlValue
}

type tomlDoc struct {
	values map[string]tomlValue    // full dotted key path -> value (outside arrays of tables)
	tables map[string]int          // [table] header path -> line
	arrays map[string][]*tomlTable // [[name]] path -> entries in file order
}

type tomlError struct {
	line int
	msg  string
}

func (e *tomlError) Error() string { return fmt.Sprintf("line %d: %s", e.line, e.msg) }

type tomlParser struct {
	s    string
	i    int
	line int
	doc  *tomlDoc

	prefix string     // current [table] path plus "." (or "")
	aot    *tomlTable // current [[array]] entry, when the header was one
	aotKey string     // its path
}

func parseTOML(data []byte) (doc *tomlDoc, err error) {
	p := &tomlParser{
		s:    strings.ReplaceAll(string(data), "\r\n", "\n"),
		line: 1,
		doc:  &tomlDoc{values: map[string]tomlValue{}, tables: map[string]int{}, arrays: map[string][]*tomlTable{}},
	}
	defer func() {
		if r := recover(); r != nil {
			te, ok := r.(*tomlError)
			if !ok {
				panic(r)
			}
			doc, err = nil, te
		}
	}()
	p.document()
	return p.doc, nil
}

func (p *tomlParser) fail(format string, args ...any) {
	panic(&tomlError{p.line, fmt.Sprintf(format, args...)})
}

func (p *tomlParser) eof() bool { return p.i >= len(p.s) }

func (p *tomlParser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.s[p.i]
}

func (p *tomlParser) next() byte {
	b := p.s[p.i]
	p.i++
	if b == '\n' {
		p.line++
	}
	return b
}

func (p *tomlParser) skipSpaces() {
	for !p.eof() && (p.peek() == ' ' || p.peek() == '\t') {
		p.i++
	}
}

// skipComment consumes a `#` comment up to (not including) the newline.
func (p *tomlParser) skipComment() {
	if p.peek() == '#' {
		for !p.eof() && p.peek() != '\n' {
			p.i++
		}
	}
}

// endOfLine requires only spaces and a comment before the newline or EOF.
func (p *tomlParser) endOfLine() {
	p.skipSpaces()
	p.skipComment()
	if !p.eof() && p.next() != '\n' {
		p.fail("expected end of line")
	}
}

// skipBlank consumes whitespace, newlines and comments (inside arrays).
func (p *tomlParser) skipBlank() {
	for !p.eof() {
		switch p.peek() {
		case ' ', '\t', '\n':
			p.next()
		case '#':
			p.skipComment()
		default:
			return
		}
	}
}

func (p *tomlParser) document() {
	for {
		p.skipBlank()
		if p.eof() {
			return
		}
		if p.peek() == '[' {
			p.header()
			continue
		}
		p.keyValue(p.store)
		p.endOfLine()
	}
}

func (p *tomlParser) header() {
	ln := p.line
	p.next()
	isArray := p.peek() == '['
	if isArray {
		p.next()
	}
	p.skipSpaces()
	key := p.key()
	p.skipSpaces()
	closers := 1
	if isArray {
		closers = 2
	}
	for range closers {
		if p.peek() != ']' {
			p.fail("unterminated table header")
		}
		p.next()
	}
	p.endOfLine()

	switch {
	case isArray:
		t := &tomlTable{line: ln, values: map[string]tomlValue{}}
		p.doc.arrays[key] = append(p.doc.arrays[key], t)
		p.aot, p.aotKey, p.prefix = t, key, ""
	case p.aot != nil && strings.HasPrefix(key, p.aotKey+"."):
		// [name.sub] after [[name]] belongs to the last entry.
		p.prefix = strings.TrimPrefix(key, p.aotKey+".") + "."
	default:
		p.doc.tables[key] = ln
		p.aot, p.aotKey, p.prefix = nil, "", key+"."
	}
}

func (p *tomlParser) store(key string, v tomlValue) {
	if p.aot != nil {
		p.aot.values[p.prefix+key] = v
		return
	}
	p.doc.values[p.prefix+key] = v
}

// keyValue parses `key = value`, handing every scalar (inline tables flattened)
// to put under its relative dotted path.
func (p *tomlParser) keyValue(put func(string, tomlValue)) {
	key := p.key()
	p.skipSpaces()
	if p.eof() || p.next() != '=' {
		p.fail("expected = after key %q", key)
	}
	p.skipSpaces()
	p.value(key, put)
}

func bareKeyChar(b byte) bool {
	return b == '_' || b == '-' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// key parses a possibly dotted, possibly quoted key into "a.b.c".
func (p *tomlParser) key() string {
	var parts []string
	for {
		p.skipSpaces()
		switch c := p.peek(); {
		case c == '"':
			parts = append(parts, p.basicString())
		case c == '\'':
			parts = append(parts, p.literalString())
		case bareKeyChar(c):
			start := p.i
			for !p.eof() && bareKeyChar(p.peek()) {
				p.i++
			}
			parts = append(parts, p.s[start:p.i])
		default:
			p.fail("invalid key")
		}
		p.skipSpaces()
		if p.peek() != '.' {
			return strings.Join(parts, ".")
		}
		p.next()
	}
}

func (p *tomlParser) value(key string, put func(string, tomlValue)) {
	ln := p.line
	switch {
	case strings.HasPrefix(p.s[p.i:], `"""`):
		put(key, tomlValue{tomlString, p.multiline(`"""`, true), ln})
	case strings.HasPrefix(p.s[p.i:], `'''`):
		put(key, tomlValue{tomlString, p.multiline(`'''`, false), ln})
	case p.peek() == '"':
		put(key, tomlValue{tomlString, p.basicString(), ln})
	case p.peek() == '\'':
		put(key, tomlValue{tomlString, p.literalString(), ln})
	case p.peek() == '[':
		start := p.i
		p.array()
		put(key, tomlValue{tomlArray, p.s[start:p.i], ln})
	case p.peek() == '{':
		p.inlineTable(key, put)
	default:
		put(key, p.bare(ln))
	}
}

func (p *tomlParser) array() {
	p.next() // [
	discard := func(string, tomlValue) {}
	for {
		p.skipBlank()
		if p.eof() {
			p.fail("unterminated array")
		}
		if p.peek() == ']' {
			p.next()
			return
		}
		p.value("", discard)
		p.skipBlank()
		switch {
		case p.peek() == ',':
			p.next()
		case p.peek() == ']':
		default:
			p.fail("expected , or ] in array")
		}
	}
}

func (p *tomlParser) inlineTable(key string, put func(string, tomlValue)) {
	p.next() // {
	sub := func(k string, v tomlValue) { put(key+"."+k, v) }
	p.skipSpaces()
	if p.peek() == '}' {
		p.next()
		return
	}
	for {
		p.skipSpaces()
		p.keyValue(sub)
		p.skipSpaces()
		switch c := p.peekOrFail("unterminated inline table"); c {
		case ',':
			p.next()
		case '}':
			p.next()
			return
		default:
			p.fail("expected , or } in inline table")
		}
	}
}

func (p *tomlParser) peekOrFail(msg string) byte {
	if p.eof() || p.peek() == '\n' {
		p.fail("%s", msg)
	}
	return p.peek()
}

var (
	tomlDateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([Tt ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?([Zz]|[+-]\d{2}:\d{2})?)?$|^\d{2}:\d{2}:\d{2}(\.\d+)?$`)
	tomlIntRe   = regexp.MustCompile(`^[+-]?(0|[1-9](_?\d)*)$|^0x[0-9A-Fa-f](_?[0-9A-Fa-f])*$|^0o[0-7](_?[0-7])*$|^0b[01](_?[01])*$`)
	tomlFloatRe = regexp.MustCompile(`^[+-]?(0|[1-9](_?\d)*)(\.\d(_?\d)*)?([eE][+-]?\d(_?\d)*)?$|^[+-]?(inf|nan)$`)
	dateTimeGap = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:`)
)

// bare reads a number, boolean or date.
func (p *tomlParser) bare(ln int) tomlValue {
	start := p.i
	for !p.eof() {
		c := p.peek()
		if c == ' ' && dateTimeGap.MatchString(p.s[start:min(len(p.s), p.i+4)]) {
			p.i++ // `1979-05-27 07:32:00`: the space separates date and time
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' || c == ',' || c == ']' || c == '}' || c == '#' {
			break
		}
		p.i++
	}
	raw := p.s[start:p.i]
	switch {
	case raw == "true" || raw == "false":
		return tomlValue{tomlBool, raw, ln}
	case tomlDateRe.MatchString(raw):
		return tomlValue{tomlDate, raw, ln}
	case tomlIntRe.MatchString(raw):
		return tomlValue{tomlInt, raw, ln}
	case tomlFloatRe.MatchString(raw):
		return tomlValue{tomlFloat, raw, ln}
	case raw == "":
		p.fail("missing value")
	}
	p.fail("invalid value %q", raw)
	return tomlValue{}
}

func (p *tomlParser) literalString() string {
	p.next() // '
	start := p.i
	for !p.eof() && p.peek() != '\'' {
		if p.peek() == '\n' {
			p.fail("unterminated literal string")
		}
		p.i++
	}
	if p.eof() {
		p.fail("unterminated literal string")
	}
	s := p.s[start:p.i]
	p.next()
	return s
}

func (p *tomlParser) basicString() string {
	p.next() // "
	var b strings.Builder
	for {
		if p.eof() || p.peek() == '\n' {
			p.fail("unterminated string")
		}
		c := p.next()
		switch c {
		case '"':
			return b.String()
		case '\\':
			p.escape(&b)
		default:
			b.WriteByte(c)
		}
	}
}

// multiline reads a """ or ”' string; basic ones honour escapes and the
// line-ending backslash. Up to two quote characters may precede the closer.
func (p *tomlParser) multiline(delim string, basic bool) string {
	p.i += 3
	if p.peek() == '\n' {
		p.next() // a newline right after the opener is trimmed
	}
	var b strings.Builder
	for {
		if p.eof() {
			p.fail("unterminated multi-line string")
		}
		if strings.HasPrefix(p.s[p.i:], delim) {
			extra := 0
			for extra < 2 && strings.HasPrefix(p.s[p.i+1+extra:], delim) {
				extra++
			}
			b.WriteString(p.s[p.i : p.i+extra])
			p.i += 3 + extra
			return b.String()
		}
		c := p.next()
		if !basic || c != '\\' {
			b.WriteByte(c)
			continue
		}
		j := p.i
		for j < len(p.s) && (p.s[j] == ' ' || p.s[j] == '\t') {
			j++
		}
		if j < len(p.s) && p.s[j] == '\n' {
			// Line-ending backslash: drop the newline and the following whitespace.
			for !p.eof() && (p.peek() == ' ' || p.peek() == '\t' || p.peek() == '\n') {
				p.next()
			}
			continue
		}
		p.escape(&b)
	}
}

func (p *tomlParser) escape(b *strings.Builder) {
	if p.eof() {
		p.fail("unterminated escape")
	}
	switch c := p.next(); c {
	case 'b':
		b.WriteByte('\b')
	case 't':
		b.WriteByte('\t')
	case 'n':
		b.WriteByte('\n')
	case 'f':
		b.WriteByte('\f')
	case 'r':
		b.WriteByte('\r')
	case 'e':
		b.WriteByte(0x1b)
	case '"', '\\':
		b.WriteByte(c)
	case 'u', 'U':
		n := 4
		if c == 'U' {
			n = 8
		}
		if p.i+n > len(p.s) {
			p.fail("short unicode escape")
		}
		r, err := strconv.ParseUint(p.s[p.i:p.i+n], 16, 32)
		if err != nil {
			p.fail("invalid unicode escape")
		}
		p.i += n
		b.WriteRune(rune(r))
	default:
		p.fail("invalid escape \\%c", c)
	}
}

// tomlNumber returns a numeric value as float64.
func tomlNumber(v tomlValue) (float64, bool) {
	raw := strings.ReplaceAll(v.str, "_", "")
	switch v.kind {
	case tomlInt:
		n, err := strconv.ParseInt(raw, 0, 64)
		return float64(n), err == nil
	case tomlFloat:
		f, err := strconv.ParseFloat(raw, 64)
		return f, err == nil
	}
	return 0, false
}

// tomlDay returns the calendar date of a TOML date/date-time, or of a string
// holding one (osv-scanner decodes both into a time.Time).
func tomlDay(v tomlValue) (time.Time, bool) {
	if v.kind != tomlDate && v.kind != tomlString {
		return time.Time{}, false
	}
	s := strings.TrimSpace(v.str)
	if v.kind == tomlString && !tomlDateRe.MatchString(s) {
		return time.Time{}, false
	}
	if len(s) < 10 {
		return time.Time{}, false // a bare local time has no date
	}
	d, err := time.Parse("2006-01-02", s[:10])
	return d, err == nil
}
