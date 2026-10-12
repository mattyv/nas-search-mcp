module Core {
  predicate TermAt(text: string, term: string, start: nat)
    requires start + |term| <= |text|
  { text[start..start+|term|] == term }

  ghost predicate Occurs(text: string, term: string) {
    exists i | 0 <= i <= |text|-|term| :: TermAt(text, term, i)
  }

  function ContainsFrom(text: string, term: string, start: nat): bool
    requires start <= |text|
    ensures ContainsFrom(text, term, start) <==>
      (exists i | start <= i <= |text| - |term| :: TermAt(text, term, i))
    ensures start == 0 ==> (ContainsFrom(text, term, start) <==> Occurs(text, term))
    decreases |text| - start
  {
    if |term| > |text| - start then false
    else if TermAt(text, term, start) then
      assert exists i | start <= i <= |text|-|term| :: TermAt(text, term, i);
      true
    else if start == |text| then false
    else ContainsFrom(text, term, start+1)
  }

  function MatchesTerms(text: string, terms: seq<string>): bool
    ensures MatchesTerms(text, terms) <==>
      (forall term <- terms :: Occurs(text, term))
    decreases |terms|
  {
    if |terms| == 0 then true else
      assert terms == [terms[0]] + terms[1..];
      assert (forall term <- terms :: Occurs(text, term)) <==>
        (Occurs(text, terms[0]) && (forall term <- terms[1..] :: Occurs(text, term)));
      ContainsFrom(text, terms[0], 0) && MatchesTerms(text, terms[1..])
  }

  predicate Before(a: (int, int), b: (int, int)) {
    a.1 > b.1 || (a.1 == b.1 && a.0 <= b.0)
  }
  predicate Ordered(items: seq<(int, int)>) {
    |items| <= 1 || (Ordered(items[1..]) &&
      forall item <- items[1..] :: Before(items[0], item))
  }
  function Insert(item: (int, int), items: seq<(int, int)>): seq<(int, int)>
    requires Ordered(items)
    ensures Ordered(Insert(item, items))
    ensures multiset(Insert(item, items)) == multiset(items) + multiset{item}
    ensures forall x <- Insert(item, items) :: x == item || x in items
    decreases |items|
  {
    if |items| == 0 then [item]
    else if Before(item, items[0]) then
      assert items == [items[0]] + items[1..];
      assert forall x <- items :: Before(item, x);
      [item] + items
    else
      assert items == [items[0]] + items[1..];
      var tail := Insert(item, items[1..]);
      assert forall x <- tail :: Before(items[0], x);
      [items[0]] + tail
  }
  function Sort(items: seq<(int, int)>): seq<(int, int)>
    ensures Ordered(Sort(items))
    ensures multiset(Sort(items)) == multiset(items)
    ensures |Sort(items)| == |items|
    decreases |items|
  {
    if |items| == 0 then [] else
      assert items == [items[0]] + items[1..];
      Insert(items[0], Sort(items[1..]))
  }
  predicate UniqueIDs(items: seq<(int,int)>) {
    (forall x <- items :: multiset(items)[x] == 1) &&
    (forall a, b | a in items && b in items :: a.0 == b.0 ==> a == b)
  }
  function SortTop(items: seq<(int, int)>, limit: nat): seq<(int, int)>
    ensures Ordered(SortTop(items, limit))
    ensures UniqueIDs(items) ==> UniqueIDs(SortTop(items, limit))
    ensures |SortTop(items, limit)| == (if limit < |items| then limit else |items|)
    ensures multiset(SortTop(items, limit)) <= multiset(items)
    ensures forall i, j | 0 <= i < |SortTop(items, limit)| <= j < |items| ::
      Before(SortTop(items, limit)[i], Sort(items)[j])
    ensures SortTop(items, limit) == Sort(items)[..(if limit < |items| then limit else |items|)]
  {
    var sorted := Sort(items);
    var count := if limit < |items| then limit else |items|;
    assert sorted == sorted[..count] + sorted[count..];
    assert multiset(sorted[..count]) <= multiset(items);
    assert forall x <- sorted[..count] :: multiset(sorted[..count])[x] > 0;
    assert UniqueIDs(items) ==>
      (forall x <- sorted[..count] :: multiset(sorted[..count])[x] == 1);
    assert Ordered(sorted[..count]) by { PrefixOrdered(sorted, count); }
    assert forall i, j | 0 <= i < count <= j < |items| :: Before(sorted[i], sorted[j]) by {
      forall i, j | 0 <= i < count <= j < |items|
        ensures Before(sorted[i], sorted[j]) {
        OrderedIndex(sorted, i, j);
      }
    }
    sorted[..count]
  }

  lemma OrderedIndex(items: seq<(int,int)>, i: nat, j: nat)
    requires Ordered(items) && i < j < |items|
    ensures Before(items[i], items[j])
    decreases i
  {
    if i == 0 {
      assert items[j] in items[1..];
    } else {
      OrderedIndex(items[1..], i-1, j-1);
    }
  }

  lemma PrefixOrdered(items: seq<(int,int)>, count: nat)
    requires Ordered(items) && count <= |items|
    ensures Ordered(items[..count])
    decreases count
  {
    if count > 1 {
      PrefixOrdered(items[1..], count-1);
      assert items[..count][1..] == items[1..count];
      assert items[1..] == items[1..count] + items[count..];
      assert forall x <- items[1..count] :: Before(items[0], x);
    }
  }
  function ValidRelative(path: string): bool {
    |path| > 0 && path[0] != '/' &&
    (forall i | 0 <= i < |path| :: path[i] != '\\' && path[i] != '\0') &&
    ValidSegments(path)
  }

  lemma RelativePathLexicalSafety(path: string)
    requires ValidRelative(path)
    ensures |path| > 0 && path[0] != '/'
    ensures '\\' !in path && '\0' !in path
    ensures ValidSegments(path)
  {}

  function ValidSegments(path: string): bool
    decreases |path|
  {
    if '/' !in path then path != "" && path != "." && path != ".."
    else var i := FirstSlash(path);
      path[..i] != "" && path[..i] != "." && path[..i] != ".." &&
      ValidSegments(path[i+1..])
  }

  function FirstSlash(path: string): nat
    requires '/' in path
    ensures FirstSlash(path) < |path|
    ensures path[FirstSlash(path)] == '/'
    decreases |path|
  {
    if path[0] == '/' then 0 else 1 + FirstSlash(path[1..])
  }

  function ClampLimit(requested: int, maximum: int): int
    requires maximum >= 1
    ensures 1 <= ClampLimit(requested, maximum) <= maximum
  {
    if requested < 1 then 1 else if requested > maximum then maximum else requested
  }

  function RankScore(lexicalRank: nat, semanticRank: nat): int {
    (if lexicalRank == 0 then 0 else 1000000 / (60 + lexicalRank)) +
    (if semanticRank == 0 then 0 else 1000000 / (60 + semanticRank))
  }


  lemma QuotientMonotonic(n: nat, small: nat, large: nat)
    requires 0 < small <= large
    ensures n / small >= n / large
  {
    assert n == large * (n / large) + n % large;
    assert large * (n / large) <= n;
    assert small * (n / large) <= large * (n / large);
    assert n == small * (n / small) + n % small;
    assert n < small * (n / small + 1);
  }

  lemma RankScoreMonotonic(aLex: nat, aSem: nat, bLex: nat, bSem: nat)
    requires 0 < aLex <= bLex && 0 < aSem <= bSem
    ensures RankScore(aLex, aSem) >= RankScore(bLex, bSem)
  {
    QuotientMonotonic(1000000, 60+aLex, 60+bLex);
    QuotientMonotonic(1000000, 60+aSem, 60+bSem);
  }

  function ShouldReplace(oldHash: string, newHash: string): bool {
    oldHash != newHash
  }

  function Chunk(text: string, size: nat, overlap: nat): seq<string>
    requires size > 0 && overlap < size
    ensures |text| == 0 <==> |Chunk(text, size, overlap)| == 0
    ensures forall c <- Chunk(text, size, overlap) :: 0 < |c| <= size
    ensures |text| > 0 ==> Chunk(text, size, overlap)[0] == text[..(if |text| < size then |text| else size)]
    decreases |text|
  {
    if |text| == 0 then []
    else if |text| <= size then [text]
    else [text[..size]] + Chunk(text[size-overlap..], size, overlap)
  }

  // Drop the repeated prefix of the recursively reconstructed suffix.
  ghost function Reassemble(chunks: seq<string>, overlap: nat): string
    decreases |chunks|
  {
    if |chunks| == 0 then ""
    else if |chunks| == 1 then chunks[0]
    else var tail := Reassemble(chunks[1..], overlap);
      chunks[0] + tail[(if overlap < |tail| then overlap else |tail|)..]
  }

  lemma ChunkCoverage(text: string, size: nat, overlap: nat)
    requires size > 0 && overlap < size
    ensures Reassemble(Chunk(text, size, overlap), overlap) == text
    decreases |text|
  {
    if |text| > size {
      ChunkCoverage(text[size-overlap..], size, overlap);
      assert text[..size] + text[size..] == text;
    }
  }

  lemma ReplacePreservesOthers<K, V>(state: map<K,V>, key: K, value: V, other: K)
    requires other in state && other != key
    ensures state[key := value][other] == state[other]
  {}

  lemma ReplaceIdempotent<K, V>(state: map<K,V>, key: K, value: V)
    ensures state[key := value][key := value] == state[key := value]
  {}

  ghost function Remove<K, V>(state: map<K,V>, key: K): map<K,V> {
    map k | k in state && k != key :: state[k]
  }

  lemma RemovePreservesOthers<K, V>(state: map<K,V>, key: K, other: K)
    requires other in state && other != key
    ensures other in Remove(state, key)
    ensures Remove(state, key)[other] == state[other]
  {}

  lemma RemoveIdempotent<K, V>(state: map<K,V>, key: K)
    ensures Remove(Remove(state, key), key) == Remove(state, key)
    ensures key !in Remove(state, key)
  {}
}
