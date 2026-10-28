// Package policy is the only hand-written bridge to the verified Dafny core.
package policy

import (
	core "github.com/mattyv/nas-search-mcp/generated/core/Core"
	"github.com/mattyv/nas-search-mcp/generated/core/dafny"
)

func ValidRelative(s string) bool {
	return core.Companion_Default___.ValidRelative(dafny.UnicodeSeqOfUtf8Bytes(s))
}
func ClampLimit(n, max int) int {
	return core.Companion_Default___.ClampLimit(dafny.IntOf(n), dafny.IntOf(max)).Int()
}
func ShouldReplace(old, new string) bool {
	return core.Companion_Default___.ShouldReplace(dafny.UnicodeSeqOfUtf8Bytes(old), dafny.UnicodeSeqOfUtf8Bytes(new))
}
func IndexedFingerprint(hash string, clip bool) string {
	return dafny.SequenceVerbatimString(core.Companion_Default___.IndexedFingerprint(dafny.UnicodeSeqOfUtf8Bytes(hash), clip), false)
}
func CanSkipUnchanged(storedStamp, currentStamp, storedFingerprint string, clip bool) bool {
	return core.Companion_Default___.CanSkipUnchanged(dafny.UnicodeSeqOfUtf8Bytes(storedStamp), dafny.UnicodeSeqOfUtf8Bytes(currentStamp), dafny.UnicodeSeqOfUtf8Bytes(storedFingerprint), clip)
}
func RankScore(lexical, semantic int) int64 {
	return core.Companion_Default___.RankScore(dafny.IntOf(lexical), dafny.IntOf(semantic)).Int64()
}
func Chunk(text string, size, overlap int) []string {
	seq := core.Companion_Default___.Chunk(dafny.UnicodeSeqOfUtf8Bytes(text), dafny.IntOf(size), dafny.IntOf(overlap))
	result := make([]string, 0, seq.Cardinality())
	for i := uint32(0); i < seq.Cardinality(); i++ {
		result = append(result, dafny.SequenceVerbatimString(seq.Select(i).(dafny.Sequence), false))
	}
	return result
}

// Top orders and bounds candidates in the verified Dafny implementation.
func Top(items [][2]int64, limit int) [][2]int64 {
	values := make([]interface{}, len(items))
	for i, item := range items {
		values[i] = dafny.TupleOf(dafny.IntOfInt64(item[0]), dafny.IntOfInt64(item[1]))
	}
	result := core.Companion_Default___.SortTop(dafny.SeqOf(values...), dafny.IntOf(limit))
	out := make([][2]int64, 0, result.Cardinality())
	for i := uint32(0); i < result.Cardinality(); i++ {
		pair := result.Select(i).(dafny.Tuple)
		out = append(out, [2]int64{(*pair.IndexInt(0)).(dafny.Int).Int64(), (*pair.IndexInt(1)).(dafny.Int).Int64()})
	}
	return out
}

// MatchesTerms uses literal substring AND matching on prepared-case input.
func MatchesTerms(text string, terms []string) bool {
	values := make([]interface{}, len(terms))
	for i, term := range terms {
		values[i] = dafny.UnicodeSeqOfUtf8Bytes(term)
	}
	return core.Companion_Default___.MatchesTerms(dafny.UnicodeSeqOfUtf8Bytes(text), dafny.SeqOf(values...))
}

// ShowInline reports whether a lowercase media type, without parameters, may render in a browser.
func ShowInline(mediaType string) bool {
	return core.Companion_Default___.ShowInline(dafny.UnicodeSeqOfUtf8Bytes(mediaType))
}

// LinkActive reports whether a link expiring at expires is usable at now.
func LinkActive(now, expires int64) bool {
	return core.Companion_Default___.LinkActive(dafny.IntOfInt64(now), dafny.IntOfInt64(expires))
}

// LoginAttemptAllowed rejects invalid counts and exhausted browser sessions.
func LoginAttemptAllowed(failures int) bool {
	return core.Companion_Default___.LoginAttemptAllowed(dafny.IntOf(failures))
}

// FailedLoginCount requires LoginAttemptAllowed(failures).
func FailedLoginCount(failures int) int {
	return core.Companion_Default___.FailedLoginCount(dafny.IntOf(failures)).Int()
}
