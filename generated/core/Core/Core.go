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

// End of class Default__
