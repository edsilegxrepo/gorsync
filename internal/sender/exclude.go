package sender

import (
	"io"
	"path/filepath"
	"strings"

	"github.com/edsilegxrepo/gorsync/internal/rsyncwire"
)

type filterRuleList struct {
	Filters []*filterRule
}

// exclude.c:add_rule
func (l *filterRuleList) addRule(fr *filterRule) {
	if strings.HasSuffix(fr.pattern, "/") {
		fr.flag |= filtruleDirectory
		fr.pattern = strings.TrimSuffix(fr.pattern, "/")
	}
	if strings.ContainsFunc(fr.pattern, func(r rune) bool {
		return r == '*' || r == '[' || r == '?'
	}) {
		fr.flag |= filtruleWild
	}
	l.Filters = append(l.Filters, fr)
}

func ParseFilterRules(rules []string) (*filterRuleList, error) {
	var l filterRuleList
	for _, rule := range rules {
		fr, err := parseFilter(rule)
		if err != nil {
			return nil, err
		}
		l.addRule(fr)
	}
	return &l, nil
}

// exclude.c:check_filter
func (l *filterRuleList) matches(name string) bool {
	for _, fr := range l.Filters {
		if fr.matches(name) {
			return fr.flag&filtruleInclude == 0
		}
	}
	return false
}

// exclude.c:recv_filter_list
func RecvFilterList(c *rsyncwire.Conn) (*filterRuleList, error) {
	var l filterRuleList
	const exclusionListEnd = 0
	for {
		length, err := c.ReadInt32()
		if err != nil {
			return nil, err
		}
		if length == exclusionListEnd {
			break
		}
		line := make([]byte, length)
		if _, err := io.ReadFull(c.Reader, line); err != nil {
			return nil, err
		}
		fr, err := parseFilter(string(line))
		if err != nil {
			return nil, err
		}
		l.addRule(fr)
	}
	return &l, nil
}

const (
	filtruleInclude = 1 << iota
	filtruleClearList
	filtruleDirectory
	filtruleWild
)

type filterRule struct {
	flag    int
	pattern string
}

// exclude.c:rule_matches
func (fr *filterRule) matches(name string) bool {
	pattern := fr.pattern
	if !strings.ContainsRune(pattern, '/') {
		name = filepath.Base(name)
	}
	if fr.flag&filtruleWild != 0 {
		return wildmatch(pattern, name)
	}
	return pattern == name
}

func wildmatch(pattern, text string) bool {
	return dowild([]byte(pattern), []byte(text))
}

func dowild(p, t []byte) bool {
	ti := 0
	for pi := 0; pi < len(p); pi++ {
		pc := p[pi]
		if ti >= len(t) && pc != '*' {
			return false
		}
		switch pc {
		case '?':
			if t[ti] == '/' {
				return false
			}
			ti++
		case '*':
			doubleStar := pi+1 < len(p) && p[pi+1] == '*'
			for pi+1 < len(p) && p[pi+1] == '*' {
				pi++
			}
			if pi == len(p)-1 {
				return doubleStar || !bytesContainsSlash(t[ti:])
			}
			for ; ti <= len(t); ti++ {
				if dowild(p[pi+1:], t[ti:]) {
					return true
				}
				if ti < len(t) && t[ti] == '/' && !doubleStar {
					return false
				}
			}
			return false
		case '[':
			if ti >= len(t) {
				return false
			}
			pi++
			negate := pi < len(p) && (p[pi] == '!' || p[pi] == '^')
			if negate {
				pi++
			}
			matched := false
			first := true
			for pi < len(p) && (p[pi] != ']' || first) {
				if pi+2 < len(p) && p[pi+1] == '-' && p[pi+2] != ']' {
					if t[ti] >= p[pi] && t[ti] <= p[pi+2] {
						matched = true
					}
					pi += 3
				} else {
					if t[ti] == p[pi] {
						matched = true
					}
					pi++
				}
				first = false
			}
			if matched == negate {
				return false
			}
			ti++
		default:
			if t[ti] != pc {
				return false
			}
			ti++
		}
	}
	return ti == len(t)
}

func bytesContainsSlash(b []byte) bool {
	for _, c := range b {
		if c == '/' {
			return true
		}
	}
	return false
}

// exclude.c:parse_filter_str / exclude.c:parse_rule_tok
func parseFilter(line string) (*filterRule, error) {
	rule := new(filterRule)

	// We only support what rsync calls XFLG_OLD_PREFIXES
	if strings.HasPrefix(line, "- ") {
		// clear include flag
		rule.flag &= ^filtruleInclude
		line = strings.TrimPrefix(line, "- ")
	} else if strings.HasPrefix(line, "+ ") {
		// set include flag
		rule.flag |= filtruleInclude
		line = strings.TrimPrefix(line, "+ ")
	} else if strings.HasPrefix(line, "!") {
		// set clear_list flag
		rule.flag |= filtruleClearList
	}

	rule.pattern = line

	return rule, nil
}
