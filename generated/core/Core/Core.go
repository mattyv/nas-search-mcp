// Package Core
// Dafny module Core compiled into Go

package Core

import (
	os "os"

	m__System "github.com/mattyv/nas-search-mcp/generated/core/System_"
	_dafny "github.com/mattyv/nas-search-mcp/generated/core/dafny"
)

var _ = os.Args
var _ _dafny.Dummy__
var _ m__System.Dummy__

type Dummy__ struct{}

// Definition of class Default__
type Default__ struct {
	dummy byte
}

func New_Default___() *Default__ {
	_this := Default__{}

	return &_this
}

type CompanionStruct_Default___ struct {
}

var Companion_Default___ = CompanionStruct_Default___{}

func (_this *Default__) Equals(other *Default__) bool {
	return _this == other
}

func (_this *Default__) EqualsGeneric(x interface{}) bool {
	other, ok := x.(*Default__)
	return ok && _this.Equals(other)
}

func (*Default__) String() string {
	return "_module.Default__"
}
func (_this *Default__) ParentTraits_() []*_dafny.TraitID {
	return [](*_dafny.TraitID){}
}

var _ _dafny.TraitOffspring = &Default__{}

func (_static *CompanionStruct_Default___) TermAt(text _dafny.Sequence, term _dafny.Sequence, start _dafny.Int) bool {
	return _dafny.Companion_Sequence_.Equal((text).Subsequence((start).Uint32(), ((start).Plus(_dafny.IntOfUint32((term).Cardinality()))).Uint32()), term)
}
func (_static *CompanionStruct_Default___) ContainsFrom(text _dafny.Sequence, term _dafny.Sequence, start _dafny.Int) bool {
	goto TAIL_CALL_START
TAIL_CALL_START:
	if (_dafny.IntOfUint32((term).Cardinality())).Cmp((_dafny.IntOfUint32((text).Cardinality())).Minus(start)) > 0 {
		return false
	} else if Companion_Default___.TermAt(text, term, start) {
		return true
	} else if (start).Cmp(_dafny.IntOfUint32((text).Cardinality())) == 0 {
		return false
	} else {
		var _in0 _dafny.Sequence = text
		_ = _in0
		var _in1 _dafny.Sequence = term
		_ = _in1
		var _in2 _dafny.Int = (start).Plus(_dafny.One)
		_ = _in2
		text = _in0
		term = _in1
		start = _in2
		goto TAIL_CALL_START
	}
}
func (_static *CompanionStruct_Default___) MatchesTerms(text _dafny.Sequence, terms _dafny.Sequence) bool {
	if (_dafny.IntOfUint32((terms).Cardinality())).Sign() == 0 {
		return true
	} else {
		return (Companion_Default___.ContainsFrom(text, (terms).Select(0).(_dafny.Sequence), _dafny.Zero)) && (Companion_Default___.MatchesTerms(text, (terms).Drop(1)))
	}
}
func (_static *CompanionStruct_Default___) Before(a _dafny.Tuple, b _dafny.Tuple) bool {
	return (((*(a).IndexInt(1)).(_dafny.Int)).Cmp((*(b).IndexInt(1)).(_dafny.Int)) > 0) || ((((*(a).IndexInt(1)).(_dafny.Int)).Cmp((*(b).IndexInt(1)).(_dafny.Int)) == 0) && (((*(a).IndexInt(0)).(_dafny.Int)).Cmp((*(b).IndexInt(0)).(_dafny.Int)) <= 0))
}
func (_static *CompanionStruct_Default___) Ordered(items _dafny.Sequence) bool {
	return ((_dafny.IntOfUint32((items).Cardinality())).Cmp(_dafny.One) <= 0) || ((Companion_Default___.Ordered((items).Drop(1))) && (_dafny.Quantifier(((items).Drop(1)).UniqueElements(), true, func(_forall_var_0 _dafny.Tuple) bool {
		var _0_item _dafny.Tuple
		_0_item = interface{}(_forall_var_0).(_dafny.Tuple)
		return !(_dafny.Companion_Sequence_.Contains((items).Drop(1), _0_item)) || (Companion_Default___.Before((items).Select(0).(_dafny.Tuple), _0_item))
	})))
}
func (_static *CompanionStruct_Default___) Insert(item _dafny.Tuple, items _dafny.Sequence) _dafny.Sequence {
	if (_dafny.IntOfUint32((items).Cardinality())).Sign() == 0 {
		return _dafny.SeqOf(item)
	} else if Companion_Default___.Before(item, (items).Select(0).(_dafny.Tuple)) {
		return _dafny.Companion_Sequence_.Concatenate(_dafny.SeqOf(item), items)
	} else {
		var _0_tail _dafny.Sequence = Companion_Default___.Insert(item, (items).Drop(1))
		_ = _0_tail
		return _dafny.Companion_Sequence_.Concatenate(_dafny.SeqOf((items).Select(0).(_dafny.Tuple)), _0_tail)
	}
}
func (_static *CompanionStruct_Default___) Sort(items _dafny.Sequence) _dafny.Sequence {
	if (_dafny.IntOfUint32((items).Cardinality())).Sign() == 0 {
		return _dafny.SeqOf()
	} else {
		return Companion_Default___.Insert((items).Select(0).(_dafny.Tuple), Companion_Default___.Sort((items).Drop(1)))
	}
}
func (_static *CompanionStruct_Default___) UniqueIDs(items _dafny.Sequence) bool {
	return (_dafny.Quantifier((items).UniqueElements(), true, func(_forall_var_0 _dafny.Tuple) bool {
		var _0_x _dafny.Tuple
		_0_x = interface{}(_forall_var_0).(_dafny.Tuple)
		return !(_dafny.Companion_Sequence_.Contains(items, _0_x)) || (((_dafny.MultiSetFromSeq(items)).Multiplicity(_0_x)).Cmp(_dafny.One) == 0)
	})) && (_dafny.Quantifier((items).UniqueElements(), true, func(_forall_var_1 _dafny.Tuple) bool {
		var _1_a _dafny.Tuple
		_1_a = interface{}(_forall_var_1).(_dafny.Tuple)
		return _dafny.Quantifier((items).UniqueElements(), true, func(_forall_var_2 _dafny.Tuple) bool {
			var _2_b _dafny.Tuple
			_2_b = interface{}(_forall_var_2).(_dafny.Tuple)
			return !((_dafny.Companion_Sequence_.Contains(items, _1_a)) && (_dafny.Companion_Sequence_.Contains(items, _2_b))) || (!(((*(_1_a).IndexInt(0)).(_dafny.Int)).Cmp((*(_2_b).IndexInt(0)).(_dafny.Int)) == 0) || ((_1_a).Equals(_2_b)))
		})
	}))
}
func (_static *CompanionStruct_Default___) SortTop(items _dafny.Sequence, limit _dafny.Int) _dafny.Sequence {
	var _0_sorted _dafny.Sequence = Companion_Default___.Sort(items)
	_ = _0_sorted
	var _1_count _dafny.Int = (func() _dafny.Int {
		if (limit).Cmp(_dafny.IntOfUint32((items).Cardinality())) < 0 {
			return limit
		}
		return _dafny.IntOfUint32((items).Cardinality())
	})()
	_ = _1_count
	return (_0_sorted).Take((_1_count).Uint32())
}
func (_static *CompanionStruct_Default___) ValidRelative(path _dafny.Sequence) bool {
	return ((((_dafny.IntOfUint32((path).Cardinality())).Sign() == 1) && (((path).Select(0).(_dafny.CodePoint)) != (_dafny.CodePoint('/')) /* dircomp */)) && (_dafny.Quantifier(_dafny.IntegerRange(_dafny.Zero, _dafny.IntOfUint32((path).Cardinality())), true, func(_forall_var_0 _dafny.Int) bool {
		var _0_i _dafny.Int
		_0_i = interface{}(_forall_var_0).(_dafny.Int)
		return !(((_0_i).Sign() != -1) && ((_0_i).Cmp(_dafny.IntOfUint32((path).Cardinality())) < 0)) || ((((path).Select((_0_i).Uint32()).(_dafny.CodePoint)) != (_dafny.CodePoint('\\')) /* dircomp */) && (((path).Select((_0_i).Uint32()).(_dafny.CodePoint)) != (_dafny.CodePoint('\u0000')) /* dircomp */))
	}))) && (Companion_Default___.ValidSegments(path))
}
func (_static *CompanionStruct_Default___) ValidSegments(path _dafny.Sequence) bool {
	if !_dafny.Companion_Sequence_.Contains(path, _dafny.CodePoint('/')) {
		return ((!_dafny.Companion_Sequence_.Equal(path, _dafny.UnicodeSeqOfUtf8Bytes(""))) && (!_dafny.Companion_Sequence_.Equal(path, _dafny.UnicodeSeqOfUtf8Bytes(".")))) && (!_dafny.Companion_Sequence_.Equal(path, _dafny.UnicodeSeqOfUtf8Bytes("..")))
	} else {
		var _0_i _dafny.Int = Companion_Default___.FirstSlash(path)
		_ = _0_i
		return (((!_dafny.Companion_Sequence_.Equal((path).Take((_0_i).Uint32()), _dafny.UnicodeSeqOfUtf8Bytes(""))) && (!_dafny.Companion_Sequence_.Equal((path).Take((_0_i).Uint32()), _dafny.UnicodeSeqOfUtf8Bytes(".")))) && (!_dafny.Companion_Sequence_.Equal((path).Take((_0_i).Uint32()), _dafny.UnicodeSeqOfUtf8Bytes("..")))) && (Companion_Default___.ValidSegments((path).Drop(((_0_i).Plus(_dafny.One)).Uint32())))
	}
}
func (_static *CompanionStruct_Default___) FirstSlash(path _dafny.Sequence) _dafny.Int {
	var _0___accumulator _dafny.Int = _dafny.Zero
	_ = _0___accumulator
	goto TAIL_CALL_START
TAIL_CALL_START:
	if ((path).Select(0).(_dafny.CodePoint)) == (_dafny.CodePoint('/')) {
		return (_dafny.Zero).Plus(_0___accumulator)
	} else {
		_0___accumulator = (_0___accumulator).Plus(_dafny.One)
		var _in0 _dafny.Sequence = (path).Drop(1)
		_ = _in0
		path = _in0
		goto TAIL_CALL_START
	}
}
func (_static *CompanionStruct_Default___) ClampLimit(requested _dafny.Int, maximum _dafny.Int) _dafny.Int {
	if (requested).Cmp(_dafny.One) < 0 {
		return _dafny.One
	} else if (requested).Cmp(maximum) > 0 {
		return maximum
	} else {
		return requested
	}
}
func (_static *CompanionStruct_Default___) ShowInline(mediaType _dafny.Sequence) bool {
	return (Companion_Default___.InlineTypes()).Contains(mediaType)
}
func (_static *CompanionStruct_Default___) LinkActive(now _dafny.Int, expires _dafny.Int) bool {
	return (now).Cmp(expires) < 0
}
func (_static *CompanionStruct_Default___) LoginAttemptAllowed(failures _dafny.Int) bool {
	return ((failures).Sign() != -1) && ((failures).Cmp(_dafny.IntOfInt64(5)) < 0)
}
func (_static *CompanionStruct_Default___) ValidPhotoPage(offset _dafny.Int, limit _dafny.Int) bool {
	return (((offset).Sign() != -1) && ((offset).Cmp(_dafny.IntOfInt64(100000)) <= 0)) && (((_dafny.One).Cmp(limit) <= 0) && ((limit).Cmp(_dafny.IntOfInt64(100)) <= 0))
}
func (_static *CompanionStruct_Default___) ValidPhotoPerson(id _dafny.Int) bool {
	return ((_dafny.One).Cmp(id) <= 0) && ((id).Cmp(_dafny.IntOfInt64(2147483647)) <= 0)
}
func (_static *CompanionStruct_Default___) PhotoDenyPath(path _dafny.Sequence) bool {
	return (_dafny.Companion_Sequence_.Equal(path, _dafny.UnicodeSeqOfUtf8Bytes("Photos"))) || (((_dafny.IntOfUint32((path).Cardinality())).Cmp(_dafny.IntOfUint32((_dafny.UnicodeSeqOfUtf8Bytes("Photos/")).Cardinality())) >= 0) && (_dafny.Companion_Sequence_.Equal((path).Take((_dafny.IntOfUint32((_dafny.UnicodeSeqOfUtf8Bytes("Photos/")).Cardinality())).Uint32()), _dafny.UnicodeSeqOfUtf8Bytes("Photos/"))))
}
func (_static *CompanionStruct_Default___) SynologyPhotosAllowed(configured bool, allow _dafny.Sequence, deny _dafny.Sequence) bool {
	return ((configured) && (_dafny.Companion_Sequence_.Contains(allow, _dafny.UnicodeSeqOfUtf8Bytes("Photos")))) && (_dafny.Quantifier((deny).UniqueElements(), true, func(_forall_var_0 _dafny.Sequence) bool {
		var _0_path _dafny.Sequence
		_0_path = interface{}(_forall_var_0).(_dafny.Sequence)
		return !(_dafny.Companion_Sequence_.Contains(deny, _0_path)) || (!(Companion_Default___.PhotoDenyPath(_0_path)))
	}))
}
func (_static *CompanionStruct_Default___) ValidOAuthRegistration(redirectCount _dafny.Int, publicAuth bool, grantsOK bool, responseOK bool, scopeOK bool, redirectsOK bool) bool {
	return (((((((_dafny.One).Cmp(redirectCount) <= 0) && ((redirectCount).Cmp(_dafny.IntOfInt64(4)) <= 0)) && (publicAuth)) && (grantsOK)) && (responseOK)) && (scopeOK)) && (redirectsOK)
}
func (_static *CompanionStruct_Default___) CanRegisterOAuthClient(existing _dafny.Int, capacity _dafny.Int) bool {
	return (((existing).Sign() != -1) && (((_dafny.One).Cmp(capacity) <= 0) && ((capacity).Cmp(_dafny.IntOfInt64(128)) <= 0))) && ((existing).Cmp(capacity) < 0)
}
func (_static *CompanionStruct_Default___) FailedLoginCount(failures _dafny.Int) _dafny.Int {
	return (failures).Plus(_dafny.One)
}
func (_static *CompanionStruct_Default___) RankScore(lexicalRank _dafny.Int, semanticRank _dafny.Int) _dafny.Int {
	return ((func() _dafny.Int {
		if (lexicalRank).Sign() == 0 {
			return _dafny.Zero
		}
		return (_dafny.IntOfInt64(1000000)).DivBy((_dafny.IntOfInt64(60)).Plus(lexicalRank))
	})()).Plus((func() _dafny.Int {
		if (semanticRank).Sign() == 0 {
			return _dafny.Zero
		}
		return (_dafny.IntOfInt64(1000000)).DivBy((_dafny.IntOfInt64(60)).Plus(semanticRank))
	})())
}
func (_static *CompanionStruct_Default___) ShouldReplace(oldHash _dafny.Sequence, newHash _dafny.Sequence) bool {
	return !_dafny.Companion_Sequence_.Equal(oldHash, newHash)
}
func (_static *CompanionStruct_Default___) IndexedFingerprint(hash _dafny.Sequence, clip bool) _dafny.Sequence {
	if clip {
		return _dafny.Companion_Sequence_.Concatenate(hash, Companion_Default___.ClipFlavour())
	} else {
		return hash
	}
}
func (_static *CompanionStruct_Default___) HasClipFlavour(fingerprint _dafny.Sequence) bool {
	return ((_dafny.IntOfUint32((fingerprint).Cardinality())).Cmp(_dafny.IntOfUint32((Companion_Default___.ClipFlavour()).Cardinality())) >= 0) && (_dafny.Companion_Sequence_.Equal((fingerprint).Drop(((_dafny.IntOfUint32((fingerprint).Cardinality())).Minus(_dafny.IntOfUint32((Companion_Default___.ClipFlavour()).Cardinality()))).Uint32()), Companion_Default___.ClipFlavour()))
}
func (_static *CompanionStruct_Default___) ContentHash(fingerprint _dafny.Sequence) _dafny.Sequence {
	if Companion_Default___.HasClipFlavour(fingerprint) {
		return (fingerprint).Take(((_dafny.IntOfUint32((fingerprint).Cardinality())).Minus(_dafny.IntOfUint32((Companion_Default___.ClipFlavour()).Cardinality()))).Uint32())
	} else {
		return fingerprint
	}
}
func (_static *CompanionStruct_Default___) CanSkipUnchanged(storedStamp _dafny.Sequence, currentStamp _dafny.Sequence, storedFingerprint _dafny.Sequence, clip bool) bool {
	return ((!_dafny.Companion_Sequence_.Equal(storedStamp, _dafny.UnicodeSeqOfUtf8Bytes(""))) && (_dafny.Companion_Sequence_.Equal(storedStamp, currentStamp))) && (_dafny.Companion_Sequence_.Equal(Companion_Default___.IndexedFingerprint(Companion_Default___.ContentHash(storedFingerprint), clip), storedFingerprint))
}
func (_static *CompanionStruct_Default___) Chunk(text _dafny.Sequence, size _dafny.Int, overlap _dafny.Int) _dafny.Sequence {
	var _0___accumulator _dafny.Sequence = _dafny.SeqOf()
	_ = _0___accumulator
	goto TAIL_CALL_START
TAIL_CALL_START:
	if (_dafny.IntOfUint32((text).Cardinality())).Sign() == 0 {
		return _dafny.Companion_Sequence_.Concatenate(_0___accumulator, _dafny.SeqOf())
	} else if (_dafny.IntOfUint32((text).Cardinality())).Cmp(size) <= 0 {
		return _dafny.Companion_Sequence_.Concatenate(_0___accumulator, _dafny.SeqOf(text))
	} else {
		_0___accumulator = _dafny.Companion_Sequence_.Concatenate(_0___accumulator, _dafny.SeqOf((text).Take((size).Uint32())))
		var _in0 _dafny.Sequence = (text).Drop(((size).Minus(overlap)).Uint32())
		_ = _in0
		var _in1 _dafny.Int = size
		_ = _in1
		var _in2 _dafny.Int = overlap
		_ = _in2
		text = _in0
		size = _in1
		overlap = _in2
		goto TAIL_CALL_START
	}
}
func (_static *CompanionStruct_Default___) ScopeChar(c _dafny.CodePoint) bool {
	return (((((_dafny.CodePoint('a')) <= (c)) && ((c) <= (_dafny.CodePoint('z')))) || (((_dafny.CodePoint('0')) <= (c)) && ((c) <= (_dafny.CodePoint('9'))))) || ((c) == (_dafny.CodePoint('-')))) || ((c) == (_dafny.CodePoint('_')))
}
func (_static *CompanionStruct_Default___) ValidScope(s _dafny.Sequence) bool {
	return ((((((_dafny.IntOfUint32((s).Cardinality())).Sign() == 1) && ((_dafny.IntOfUint32((s).Cardinality())).Cmp(_dafny.IntOfInt64(128)) <= 0)) && (((s).Select(0).(_dafny.CodePoint)) != (_dafny.CodePoint('/')) /* dircomp */)) && (((s).Select(((_dafny.IntOfUint32((s).Cardinality())).Minus(_dafny.One)).Uint32()).(_dafny.CodePoint)) != (_dafny.CodePoint('/')) /* dircomp */)) && (_dafny.Quantifier(_dafny.IntegerRange(_dafny.Zero, _dafny.IntOfUint32((s).Cardinality())), true, func(_forall_var_0 _dafny.Int) bool {
		var _0_i _dafny.Int
		_0_i = interface{}(_forall_var_0).(_dafny.Int)
		return !(((_0_i).Sign() != -1) && ((_0_i).Cmp(_dafny.IntOfUint32((s).Cardinality())) < 0)) || ((Companion_Default___.ScopeChar((s).Select((_0_i).Uint32()).(_dafny.CodePoint))) || (((s).Select((_0_i).Uint32()).(_dafny.CodePoint)) == (_dafny.CodePoint('/'))))
	}))) && (_dafny.Quantifier(_dafny.IntegerRange(_dafny.Zero, (_dafny.IntOfUint32((s).Cardinality())).Minus(_dafny.One)), true, func(_forall_var_1 _dafny.Int) bool {
		var _1_i _dafny.Int
		_1_i = interface{}(_forall_var_1).(_dafny.Int)
		return !(((_1_i).Sign() != -1) && ((_1_i).Cmp((_dafny.IntOfUint32((s).Cardinality())).Minus(_dafny.One)) < 0)) || (!((((s).Select((_1_i).Uint32()).(_dafny.CodePoint)) == (_dafny.CodePoint('/'))) && (((s).Select(((_1_i).Plus(_dafny.One)).Uint32()).(_dafny.CodePoint)) == (_dafny.CodePoint('/')))))
	}))
}
func (_static *CompanionStruct_Default___) ScopeWithin(scope _dafny.Sequence, root _dafny.Sequence) bool {
	return (_dafny.Companion_Sequence_.Equal(scope, root)) || ((((_dafny.IntOfUint32((root).Cardinality())).Cmp(_dafny.IntOfUint32((scope).Cardinality())) < 0) && (_dafny.Companion_Sequence_.Equal((scope).Take((_dafny.IntOfUint32((root).Cardinality())).Uint32()), root))) && (((scope).Select((_dafny.IntOfUint32((root).Cardinality())).Uint32()).(_dafny.CodePoint)) == (_dafny.CodePoint('/'))))
}
func (_static *CompanionStruct_Default___) ScopeVisible(scope _dafny.Sequence, roots _dafny.Sequence) bool {
	return _dafny.Quantifier(_dafny.IntegerRange(_dafny.Zero, _dafny.IntOfUint32((roots).Cardinality())), false, func(_exists_var_0 _dafny.Int) bool {
		var _0_i _dafny.Int
		_0_i = interface{}(_exists_var_0).(_dafny.Int)
		return (((_0_i).Sign() != -1) && ((_0_i).Cmp(_dafny.IntOfUint32((roots).Cardinality())) < 0)) && (Companion_Default___.ScopeWithin(scope, (roots).Select((_0_i).Uint32()).(_dafny.Sequence)))
	})
}
func (_static *CompanionStruct_Default___) ValidMemoryStatus(status _dafny.Sequence) bool {
	return (_dafny.SetOf(_dafny.UnicodeSeqOfUtf8Bytes("candidate"), _dafny.UnicodeSeqOfUtf8Bytes("accepted"), _dafny.UnicodeSeqOfUtf8Bytes("disputed"), _dafny.UnicodeSeqOfUtf8Bytes("rejected"), _dafny.UnicodeSeqOfUtf8Bytes("superseded"), _dafny.UnicodeSeqOfUtf8Bytes("retracted"))).Contains(status)
}
func (_static *CompanionStruct_Default___) IsCurrentMemory(status _dafny.Sequence) bool {
	return _dafny.Companion_Sequence_.Equal(status, _dafny.UnicodeSeqOfUtf8Bytes("accepted"))
}
func (_static *CompanionStruct_Default___) TerminalMemory(status _dafny.Sequence) bool {
	return ((_dafny.Companion_Sequence_.Equal(status, _dafny.UnicodeSeqOfUtf8Bytes("rejected"))) || (_dafny.Companion_Sequence_.Equal(status, _dafny.UnicodeSeqOfUtf8Bytes("superseded")))) || (_dafny.Companion_Sequence_.Equal(status, _dafny.UnicodeSeqOfUtf8Bytes("retracted")))
}
func (_static *CompanionStruct_Default___) ValidMemoryTransition(from _dafny.Sequence, to _dafny.Sequence) bool {
	return (((_dafny.Companion_Sequence_.Equal(from, _dafny.UnicodeSeqOfUtf8Bytes("candidate"))) && (((_dafny.Companion_Sequence_.Equal(to, _dafny.UnicodeSeqOfUtf8Bytes("accepted"))) || (_dafny.Companion_Sequence_.Equal(to, _dafny.UnicodeSeqOfUtf8Bytes("rejected")))) || (_dafny.Companion_Sequence_.Equal(to, _dafny.UnicodeSeqOfUtf8Bytes("disputed"))))) || ((_dafny.Companion_Sequence_.Equal(from, _dafny.UnicodeSeqOfUtf8Bytes("accepted"))) && (((_dafny.Companion_Sequence_.Equal(to, _dafny.UnicodeSeqOfUtf8Bytes("superseded"))) || (_dafny.Companion_Sequence_.Equal(to, _dafny.UnicodeSeqOfUtf8Bytes("retracted")))) || (_dafny.Companion_Sequence_.Equal(to, _dafny.UnicodeSeqOfUtf8Bytes("disputed")))))) || ((_dafny.Companion_Sequence_.Equal(from, _dafny.UnicodeSeqOfUtf8Bytes("disputed"))) && (((_dafny.Companion_Sequence_.Equal(to, _dafny.UnicodeSeqOfUtf8Bytes("accepted"))) || (_dafny.Companion_Sequence_.Equal(to, _dafny.UnicodeSeqOfUtf8Bytes("rejected")))) || (_dafny.Companion_Sequence_.Equal(to, _dafny.UnicodeSeqOfUtf8Bytes("retracted")))))
}
func (_static *CompanionStruct_Default___) AssistantMayTransition(from _dafny.Sequence, to _dafny.Sequence) bool {
	return (Companion_Default___.ValidMemoryTransition(from, to)) && (_dafny.Companion_Sequence_.Equal(to, _dafny.UnicodeSeqOfUtf8Bytes("disputed")))
}
func (_static *CompanionStruct_Default___) ExternalClassificationAllowed(configured bool, userAttested bool, scope _dafny.Sequence, external _dafny.Sequence) bool {
	return ((configured) && (!(userAttested))) && (Companion_Default___.ScopeVisible(scope, external))
}
func (_static *CompanionStruct_Default___) ValidMemoryCategory(c _dafny.Sequence) bool {
	return (_dafny.SetOf(_dafny.UnicodeSeqOfUtf8Bytes("fact"), _dafny.UnicodeSeqOfUtf8Bytes("preference"), _dafny.UnicodeSeqOfUtf8Bytes("decision"), _dafny.UnicodeSeqOfUtf8Bytes("constraint"), _dafny.UnicodeSeqOfUtf8Bytes("lesson"))).Contains(c)
}
func (_static *CompanionStruct_Default___) ProposesMemory(eventType _dafny.Sequence) bool {
	return (_dafny.SetOf(_dafny.UnicodeSeqOfUtf8Bytes("decision"), _dafny.UnicodeSeqOfUtf8Bytes("observation"), _dafny.UnicodeSeqOfUtf8Bytes("correction"), _dafny.UnicodeSeqOfUtf8Bytes("manual_note"))).Contains(eventType)
}
func (_static *CompanionStruct_Default___) DefaultMemoryCategory(eventType _dafny.Sequence) _dafny.Sequence {
	if _dafny.Companion_Sequence_.Equal(eventType, _dafny.UnicodeSeqOfUtf8Bytes("decision")) {
		return _dafny.UnicodeSeqOfUtf8Bytes("decision")
	} else {
		return _dafny.UnicodeSeqOfUtf8Bytes("fact")
	}
}
func (_static *CompanionStruct_Default___) ValidEventType(t _dafny.Sequence) bool {
	return (_dafny.SetOf(_dafny.UnicodeSeqOfUtf8Bytes("observation"), _dafny.UnicodeSeqOfUtf8Bytes("discussion"), _dafny.UnicodeSeqOfUtf8Bytes("exploration"), _dafny.UnicodeSeqOfUtf8Bytes("decision"), _dafny.UnicodeSeqOfUtf8Bytes("action"), _dafny.UnicodeSeqOfUtf8Bytes("result"), _dafny.UnicodeSeqOfUtf8Bytes("correction"), _dafny.UnicodeSeqOfUtf8Bytes("checkpoint"), _dafny.UnicodeSeqOfUtf8Bytes("manual_note"))).Contains(t)
}
func (_static *CompanionStruct_Default___) ValidEventStatus(s _dafny.Sequence) bool {
	return (_dafny.SetOf(_dafny.UnicodeSeqOfUtf8Bytes("proposed"), _dafny.UnicodeSeqOfUtf8Bytes("confirmed"), _dafny.UnicodeSeqOfUtf8Bytes("unverified"), _dafny.UnicodeSeqOfUtf8Bytes("superseded"))).Contains(s)
}
func (_static *CompanionStruct_Default___) AssistantMayAppendType(t _dafny.Sequence) bool {
	return (Companion_Default___.ValidEventType(t)) && (!_dafny.Companion_Sequence_.Equal(t, _dafny.UnicodeSeqOfUtf8Bytes("checkpoint")))
}
func (_static *CompanionStruct_Default___) MaySupersede(newActorIsUser bool, oldStatus _dafny.Sequence) bool {
	return (newActorIsUser) || (!_dafny.Companion_Sequence_.Equal(oldStatus, _dafny.UnicodeSeqOfUtf8Bytes("confirmed")))
}
func (_static *CompanionStruct_Default___) SocketMethodAllowed(name _dafny.Sequence) bool {
	return (!_dafny.Companion_Sequence_.Equal(name, _dafny.UnicodeSeqOfUtf8Bytes("ForgetEvent"))) && (!_dafny.Companion_Sequence_.Equal(name, _dafny.UnicodeSeqOfUtf8Bytes("ForgetMemory")))
}
func (_static *CompanionStruct_Default___) CorrectionMayCarryStatement(to _dafny.Sequence) bool {
	return _dafny.Companion_Sequence_.Equal(to, _dafny.UnicodeSeqOfUtf8Bytes("superseded"))
}
func (_static *CompanionStruct_Default___) ScopeWideContextCategory(category _dafny.Sequence) bool {
	return (_dafny.Companion_Sequence_.Equal(category, _dafny.UnicodeSeqOfUtf8Bytes("preference"))) || (_dafny.Companion_Sequence_.Equal(category, _dafny.UnicodeSeqOfUtf8Bytes("constraint")))
}
func (_static *CompanionStruct_Default___) DefaultEventStatus(actorIsUser bool, eventType _dafny.Sequence) _dafny.Sequence {
	if actorIsUser {
		return _dafny.UnicodeSeqOfUtf8Bytes("confirmed")
	} else if _dafny.Companion_Sequence_.Equal(eventType, _dafny.UnicodeSeqOfUtf8Bytes("exploration")) {
		return _dafny.UnicodeSeqOfUtf8Bytes("proposed")
	} else {
		return _dafny.UnicodeSeqOfUtf8Bytes("unverified")
	}
}
func (_static *CompanionStruct_Default___) EventStatusAllowed(actorIsUser bool, status _dafny.Sequence) bool {
	return (Companion_Default___.ValidEventStatus(status)) && ((actorIsUser) || (!_dafny.Companion_Sequence_.Equal(status, _dafny.UnicodeSeqOfUtf8Bytes("confirmed"))))
}
func (_static *CompanionStruct_Default___) IsUnconfirmedMemory(status _dafny.Sequence) bool {
	return (_dafny.Companion_Sequence_.Equal(status, _dafny.UnicodeSeqOfUtf8Bytes("candidate"))) || (_dafny.Companion_Sequence_.Equal(status, _dafny.UnicodeSeqOfUtf8Bytes("disputed")))
}
func (_static *CompanionStruct_Default___) ContextBudget(requested _dafny.Int) _dafny.Int {
	if (requested).Sign() != 1 {
		return _dafny.IntOfInt64(1500)
	} else if (requested).Cmp(_dafny.IntOfInt64(200)) < 0 {
		return _dafny.IntOfInt64(200)
	} else if (requested).Cmp(_dafny.IntOfInt64(8000)) > 0 {
		return _dafny.IntOfInt64(8000)
	} else {
		return requested
	}
}
func (_static *CompanionStruct_Default___) OrphanedAfterForget(evidence _dafny.Sequence, forgotten _dafny.Sequence) bool {
	return ((_dafny.IntOfUint32((evidence).Cardinality())).Sign() == 1) && (_dafny.Quantifier(_dafny.IntegerRange(_dafny.Zero, _dafny.IntOfUint32((evidence).Cardinality())), true, func(_forall_var_0 _dafny.Int) bool {
		var _0_i _dafny.Int
		_0_i = interface{}(_forall_var_0).(_dafny.Int)
		return !(((_0_i).Sign() != -1) && ((_0_i).Cmp(_dafny.IntOfUint32((evidence).Cardinality())) < 0)) || (_dafny.Companion_Sequence_.Equal((evidence).Select((_0_i).Uint32()).(_dafny.Sequence), forgotten))
	}))
}
func (_static *CompanionStruct_Default___) CheckpointWrite(current _dafny.Int, expected _dafny.Int) _dafny.Tuple {
	if (expected).Cmp(current) == 0 {
		return _dafny.TupleOf(true, (current).Plus(_dafny.One))
	} else {
		return _dafny.TupleOf(false, current)
	}
}
func (_static *CompanionStruct_Default___) SumCosts(costs _dafny.Sequence, idx _dafny.Sequence) _dafny.Int {
	var _0___accumulator _dafny.Int = _dafny.Zero
	_ = _0___accumulator
	goto TAIL_CALL_START
TAIL_CALL_START:
	if (_dafny.IntOfUint32((idx).Cardinality())).Sign() == 0 {
		return (_dafny.Zero).Plus(_0___accumulator)
	} else {
		_0___accumulator = (_0___accumulator).Plus((costs).Select(((idx).Select(0).(_dafny.Int)).Uint32()).(_dafny.Int))
		var _in0 _dafny.Sequence = costs
		_ = _in0
		var _in1 _dafny.Sequence = (idx).Drop(1)
		_ = _in1
		costs = _in0
		idx = _in1
		goto TAIL_CALL_START
	}
}
func (_static *CompanionStruct_Default___) TotalFrom(costs _dafny.Sequence, start _dafny.Int) _dafny.Int {
	var _0___accumulator _dafny.Int = _dafny.Zero
	_ = _0___accumulator
	goto TAIL_CALL_START
TAIL_CALL_START:
	if (start).Cmp(_dafny.IntOfUint32((costs).Cardinality())) == 0 {
		return (_dafny.Zero).Plus(_0___accumulator)
	} else {
		_0___accumulator = (_0___accumulator).Plus((costs).Select((start).Uint32()).(_dafny.Int))
		var _in0 _dafny.Sequence = costs
		_ = _in0
		var _in1 _dafny.Int = (start).Plus(_dafny.One)
		_ = _in1
		costs = _in0
		start = _in1
		goto TAIL_CALL_START
	}
}
func (_static *CompanionStruct_Default___) PackFrom(costs _dafny.Sequence, remaining _dafny.Int, start _dafny.Int) _dafny.Sequence {
	if (start).Cmp(_dafny.IntOfUint32((costs).Cardinality())) == 0 {
		return _dafny.SeqOf()
	} else if ((costs).Select((start).Uint32()).(_dafny.Int)).Cmp(remaining) <= 0 {
		var _0_rest _dafny.Sequence = Companion_Default___.PackFrom(costs, (remaining).Minus((costs).Select((start).Uint32()).(_dafny.Int)), (start).Plus(_dafny.One))
		_ = _0_rest
		return _dafny.Companion_Sequence_.Concatenate(_dafny.SeqOf(start), _0_rest)
	} else {
		return Companion_Default___.PackFrom(costs, remaining, (start).Plus(_dafny.One))
	}
}
func (_static *CompanionStruct_Default___) PackBudget(costs _dafny.Sequence, budget _dafny.Int) _dafny.Sequence {
	return Companion_Default___.PackFrom(costs, budget, _dafny.Zero)
}
func (_static *CompanionStruct_Default___) ValidThresholds(acceptAt _dafny.Int, rejectAt _dafny.Int) bool {
	return (((rejectAt).Sign() != -1) && ((rejectAt).Cmp(acceptAt) < 0)) && ((acceptAt).Cmp(_dafny.IntOfInt64(1000)) <= 0)
}
func (_static *CompanionStruct_Default___) RouteCandidate(p _dafny.Int, acceptAt _dafny.Int, rejectAt _dafny.Int) _dafny.Sequence {
	if ((!(Companion_Default___.ValidThresholds(acceptAt, rejectAt))) || ((p).Sign() == -1)) || ((p).Cmp(_dafny.IntOfInt64(1000)) > 0) {
		return _dafny.UnicodeSeqOfUtf8Bytes("review")
	} else if (p).Cmp(acceptAt) >= 0 {
		return _dafny.UnicodeSeqOfUtf8Bytes("accept")
	} else if (p).Cmp(rejectAt) <= 0 {
		return _dafny.UnicodeSeqOfUtf8Bytes("reject")
	} else {
		return _dafny.UnicodeSeqOfUtf8Bytes("review")
	}
}
func (_static *CompanionStruct_Default___) InitialMemoryStatus(userAttested bool, p _dafny.Int, acceptAt _dafny.Int, rejectAt _dafny.Int) _dafny.Sequence {
	if userAttested {
		return _dafny.UnicodeSeqOfUtf8Bytes("accepted")
	} else {
		var _0_route _dafny.Sequence = Companion_Default___.RouteCandidate(p, acceptAt, rejectAt)
		_ = _0_route
		if _dafny.Companion_Sequence_.Equal(_0_route, _dafny.UnicodeSeqOfUtf8Bytes("accept")) {
			return _dafny.UnicodeSeqOfUtf8Bytes("accepted")
		} else if _dafny.Companion_Sequence_.Equal(_0_route, _dafny.UnicodeSeqOfUtf8Bytes("reject")) {
			return _dafny.UnicodeSeqOfUtf8Bytes("rejected")
		} else {
			return _dafny.UnicodeSeqOfUtf8Bytes("candidate")
		}
	}
}
func (_static *CompanionStruct_Default___) PasskeyRequired(mode _dafny.Sequence, registered _dafny.Int) bool {
	return !((_dafny.Companion_Sequence_.Equal(mode, _dafny.UnicodeSeqOfUtf8Bytes("off"))) || ((_dafny.Companion_Sequence_.Equal(mode, _dafny.UnicodeSeqOfUtf8Bytes("enroll"))) && ((registered).Sign() != 1)))
}
func (_static *CompanionStruct_Default___) DefaultPasskeyMode(issuerHostIsIP bool) _dafny.Sequence {
	if issuerHostIsIP {
		return _dafny.UnicodeSeqOfUtf8Bytes("off")
	} else {
		return _dafny.UnicodeSeqOfUtf8Bytes("enroll")
	}
}
func (_static *CompanionStruct_Default___) ValidPasskeyMode(mode _dafny.Sequence) bool {
	return (_dafny.SetOf(_dafny.UnicodeSeqOfUtf8Bytes("off"), _dafny.UnicodeSeqOfUtf8Bytes("enroll"), _dafny.UnicodeSeqOfUtf8Bytes("required"))).Contains(mode)
}
func (_static *CompanionStruct_Default___) PasskeyStep(mode _dafny.Sequence, registered _dafny.Int) _dafny.Sequence {
	if !(Companion_Default___.PasskeyRequired(mode, registered)) {
		if _dafny.Companion_Sequence_.Equal(mode, _dafny.UnicodeSeqOfUtf8Bytes("enroll")) {
			return _dafny.UnicodeSeqOfUtf8Bytes("enroll_optional")
		} else {
			return _dafny.UnicodeSeqOfUtf8Bytes("complete")
		}
	} else if (registered).Sign() != 1 {
		return _dafny.UnicodeSeqOfUtf8Bytes("enroll_required")
	} else {
		return _dafny.UnicodeSeqOfUtf8Bytes("assert")
	}
}
func (_static *CompanionStruct_Default___) PasskeyRemovalAllowed(mode _dafny.Sequence, registered _dafny.Int) bool {
	return ((registered).Cmp(_dafny.One) >= 0) && (!((_dafny.Companion_Sequence_.Equal(mode, _dafny.UnicodeSeqOfUtf8Bytes("required"))) && ((registered).Cmp(_dafny.One) <= 0)))
}
func (_static *CompanionStruct_Default___) CanRegisterPasskey(registered _dafny.Int, capacity _dafny.Int) bool {
	return (((registered).Sign() != -1) && (((_dafny.One).Cmp(capacity) <= 0) && ((capacity).Cmp(_dafny.IntOfInt64(32)) <= 0))) && ((registered).Cmp(capacity) < 0)
}
func (_static *CompanionStruct_Default___) PasskeySignCountValid(stored _dafny.Int, received _dafny.Int) bool {
	return (((stored).Sign() != -1) && ((received).Sign() != -1)) && ((((stored).Sign() == 0) && ((received).Sign() == 0)) || ((received).Cmp(stored) > 0))
}
func (_static *CompanionStruct_Default___) EditorAllowed(login _dafny.Sequence, owner _dafny.Sequence) bool {
	return (!_dafny.Companion_Sequence_.Equal(owner, _dafny.UnicodeSeqOfUtf8Bytes(""))) && (_dafny.Companion_Sequence_.Equal(login, owner))
}
func (_static *CompanionStruct_Default___) InlineTypes() _dafny.Set {
	return _dafny.SetOf(_dafny.UnicodeSeqOfUtf8Bytes("application/pdf"), _dafny.UnicodeSeqOfUtf8Bytes("image/jpeg"), _dafny.UnicodeSeqOfUtf8Bytes("image/png"), _dafny.UnicodeSeqOfUtf8Bytes("image/gif"), _dafny.UnicodeSeqOfUtf8Bytes("image/webp"), _dafny.UnicodeSeqOfUtf8Bytes("text/plain"))
}
func (_static *CompanionStruct_Default___) ClipFlavour() _dafny.Sequence {
	return _dafny.UnicodeSeqOfUtf8Bytes(";clip")
}

// End of class Default__
