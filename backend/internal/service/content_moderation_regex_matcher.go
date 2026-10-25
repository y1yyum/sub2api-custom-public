package service

import (
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	maxContentModerationBlockedRegexps     = 500
	maxContentModerationBlockedRegexpRunes = 255

	// contentModerationMatchedRuleMaxRunes mirrors the VARCHAR(255) matched_keyword column.
	contentModerationMatchedRuleMaxRunes = 255

	contentModerationInvalidBlockedRegexpReason = "INVALID_CONTENT_MODERATION_BLOCKED_REGEXP"

	contentModerationMatchTypeKeyword = "keyword"
	contentModerationMatchTypeRegex   = "regex"
)

// contentModerationRegexMatcher finds the first matching rule in configuration
// order. Go regexp has no DFA, so one scan per rule costs rules x text; instead a
// segment tree of capture-free unions answers a miss with one root scan and
// locates the first matching rule with about log2(rules) further scans.
type contentModerationRegexMatcher struct {
	patterns   []string
	compiled   []*regexp.Regexp
	root       *contentModerationRegexNode
	standalone []int
}

// contentModerationRegexNode covers a contiguous range of rules; leaves hold one
// rule. union is nil when the range failed to compile as one expression; that
// range is then resolved through its children.
type contentModerationRegexNode struct {
	union       *regexp.Regexp
	rule        int
	left, right *contentModerationRegexNode
}

// normalizeBlockedRegexps trims, drops empty entries and removes exact duplicates
// while preserving order. It never truncates: limits are enforced by validation.
func normalizeBlockedRegexps(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		pattern := strings.TrimSpace(raw)
		if pattern == "" {
			continue
		}
		if _, ok := seen[pattern]; ok {
			continue
		}
		seen[pattern] = struct{}{}
		out = append(out, pattern)
	}
	return out
}

// validateBlockedRegexps enforces the save-path contract on an already normalized
// list. Per-rule errors carry the 1-based ordinal, the rule text, the failure kind
// and the RE2 parser detail as metadata so the admin UI can name the offending
// textarea line in its own words.
func validateBlockedRegexps(patterns []string) error {
	if len(patterns) > maxContentModerationBlockedRegexps {
		return infraerrors.BadRequest(contentModerationInvalidBlockedRegexpReason,
			fmt.Sprintf("正则规则最多 %d 条", maxContentModerationBlockedRegexps))
	}
	for index, pattern := range patterns {
		ordinal := index + 1
		if utf8.RuneCountInString(pattern) > maxContentModerationBlockedRegexpRunes {
			return blockedRegexpRuleError(ordinal, pattern, blockedRegexpErrorKindTooLong, "",
				fmt.Sprintf("第 %d 条正则超过 %d 个字符", ordinal, maxContentModerationBlockedRegexpRunes))
		}
		if _, err := regexp.Compile(pattern); err != nil {
			message := fmt.Sprintf("第 %d 条正则无效：%v", ordinal, err)
			if quoted := "`" + pattern + "`"; !strings.Contains(message, quoted) {
				message += "，规则原文：" + quoted
			}
			return blockedRegexpRuleError(ordinal, pattern, blockedRegexpErrorKindInvalidSyntax, err.Error(), message)
		}
	}
	return nil
}

const (
	blockedRegexpErrorKindTooLong       = "too_long"
	blockedRegexpErrorKindInvalidSyntax = "invalid_syntax"
)

func blockedRegexpRuleError(ordinal int, pattern, kind, detail, message string) error {
	metadata := map[string]string{"ordinal": strconv.Itoa(ordinal), "pattern": pattern, "kind": kind}
	if detail != "" {
		metadata["detail"] = detail
	}
	return infraerrors.BadRequest(contentModerationInvalidBlockedRegexpReason, message).WithMetadata(metadata)
}

// newContentModerationRegexMatcher compiles patterns once per config load. Invalid
// stored entries are skipped so a bad legacy rule cannot disable moderation.
func newContentModerationRegexMatcher(patterns []string) *contentModerationRegexMatcher {
	matcher := &contentModerationRegexMatcher{}
	for index, pattern := range patterns {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			slog.Warn("content_moderation.blocked_regexp_invalid",
				"index", index+1,
				"pattern", pattern,
				"error", err)
			continue
		}
		matcher.patterns = append(matcher.patterns, pattern)
		matcher.compiled = append(matcher.compiled, compiled)
	}
	if len(matcher.compiled) == 0 {
		return nil
	}
	matcher.buildTree()
	return matcher
}

// buildTree unions rules as (?:pi)|(?:pi+1)|...; a group scopes inline flags such
// as (?i), so each alternative keeps its own semantics. An unterminated \Q would
// swallow the following alternatives, so such rules stay standalone.
func (m *contentModerationRegexMatcher) buildTree() {
	members := make([]int, 0, len(m.patterns))
	for index, pattern := range m.patterns {
		if strings.Contains(pattern, `\Q`) {
			m.standalone = append(m.standalone, index)
			continue
		}
		members = append(members, index)
	}
	if len(members) == 0 {
		return
	}
	warned := false
	m.root = m.buildNode(members, &warned)
}

func (m *contentModerationRegexMatcher) buildNode(members []int, warned *bool) *contentModerationRegexNode {
	if len(members) == 1 {
		return &contentModerationRegexNode{union: m.compiled[members[0]], rule: members[0]}
	}
	alternatives := make([]string, len(members))
	for i, index := range members {
		alternatives[i] = "(?:" + m.patterns[index] + ")"
	}
	union, err := regexp.Compile(strings.Join(alternatives, "|"))
	if err != nil {
		union = nil
		if !*warned {
			*warned = true
			slog.Warn("content_moderation.blocked_regexp_union_unavailable", "rules", len(members), "error", err)
		}
	}
	middle := len(members) / 2
	return &contentModerationRegexNode{
		union: union,
		rule:  -1,
		left:  m.buildNode(members[:middle], warned),
		right: m.buildNode(members[middle:], warned),
	}
}

// first returns the first rule in configuration order under n that matches, or
// -1. known means an ancestor already proved that this range contains a match.
func (n *contentModerationRegexNode) first(text string, known bool) int {
	if !known && n.union != nil {
		if !n.union.MatchString(text) {
			return -1
		}
		known = true
	}
	if n.rule >= 0 {
		return n.rule
	}
	if rule := n.left.first(text, false); rule >= 0 {
		return rule
	}
	return n.right.first(text, known)
}

// Match returns the first rule in configuration order that matches text.
// Empty text never matches, even for rules such as ^$ that accept the empty
// string, so a request without user text is not blocked (as with keywords).
// Standalone (\Q) rules are checked individually, and only while their index is
// below the tree hit, so the combined result keeps configuration order.
func (m *contentModerationRegexMatcher) Match(text string) (string, bool) {
	if m == nil || text == "" {
		return "", false
	}
	best := -1
	if m.root != nil {
		best = m.root.first(text, false)
	}
	for _, index := range m.standalone {
		if best >= 0 && index > best {
			break
		}
		if m.compiled[index].MatchString(text) {
			best = index
			break
		}
	}
	if best < 0 {
		return "", false
	}
	return m.patterns[best], true
}

func (s *contentModerationRuntimeSnapshot) matchBlockedRegexp(text string) (string, bool) {
	if s == nil {
		return "", false
	}
	return s.regexMatcher.Match(text)
}

func truncateContentModerationMatchedRule(rule string) string {
	return trimRunes(rule, contentModerationMatchedRuleMaxRunes)
}
