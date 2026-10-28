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

  // Passive formats a browser displays without running page scripts.
  // Everything else is served as an opaque download.
  const InlineTypes: set<string> := {"application/pdf", "image/jpeg", "image/png", "image/gif", "image/webp", "text/plain"}

  function ShowInline(mediaType: string): bool
    ensures ShowInline(mediaType) <==> mediaType in InlineTypes
  {
    mediaType in InlineTypes
  }

  lemma ActiveContentNeverInline()
    ensures !ShowInline("text/html") && !ShowInline("application/xhtml+xml")
    ensures !ShowInline("image/svg+xml") && !ShowInline("text/xml") && !ShowInline("application/xml")
    ensures !ShowInline("text/javascript") && !ShowInline("application/octet-stream") && !ShowInline("")
  {}

  // A download link is usable strictly before its expiry instant.
  function LinkActive(now: int, expires: int): bool
    ensures LinkActive(now, expires) <==> now < expires
  {
    now < expires
  }

  // Each expiring browser login has its own bounded failure counter.
  function LoginAttemptAllowed(failures: int): bool
    ensures LoginAttemptAllowed(failures) <==> 0 <= failures < 5
  {
    0 <= failures < 5
  }

  // Input limits for the read-only Photos protocol adapter. Integer arguments
  // are unrestricted so malformed negative/large requests fail closed.
  function ValidPhotoPage(offset: int, limit: int): bool
    ensures ValidPhotoPage(offset, limit) <==>
      0 <= offset <= 100000 && 1 <= limit <= 100
  {
    0 <= offset <= 100000 && 1 <= limit <= 100
  }

  function ValidPhotoPerson(id: int): bool
    ensures ValidPhotoPerson(id) <==> 1 <= id <= 2147483647
  {
    1 <= id <= 2147483647
  }

  lemma PhotoRequestBounds(offset: int, limit: int, person: int)
    ensures (ValidPhotoPage(offset, limit) && ValidPhotoPerson(person)) <==>
      (0 <= offset <= 100000 && 1 <= limit <= 100 && 1 <= person <= 2147483647)
  {}

  lemma InvalidPhotoRequestRejected(offset: int, limit: int, person: int)
    requires offset < 0 || offset > 100000 || limit < 1 || limit > 100 ||
      person < 1 || person > 2147483647
    ensures !(ValidPhotoPage(offset, limit) && ValidPhotoPerson(person))
  {}

  function PhotoDenyPath(path: string): bool
    ensures PhotoDenyPath(path) <==>
      (path == "Photos" || (|path| >= |"Photos/"| && path[..|"Photos/"|] == "Photos/"))
  {
    path == "Photos" || (|path| >= |"Photos/"| && path[..|"Photos/"|] == "Photos/")
  }

  function SynologyPhotosAllowed(configured: bool, allow: seq<string>, deny: seq<string>): bool
    ensures SynologyPhotosAllowed(configured, allow, deny) <==>
      (configured && "Photos" in allow && (forall path <- deny :: !PhotoDenyPath(path)))
  {
    configured && "Photos" in allow && (forall path <- deny :: !PhotoDenyPath(path))
  }

  lemma PhotosRequireExplicitRoot(configured: bool, allow: seq<string>, deny: seq<string>)
    ensures SynologyPhotosAllowed(configured, allow, deny) ==> configured && "Photos" in allow
    ensures (!configured || "Photos" !in allow) ==> !SynologyPhotosAllowed(configured, allow, deny)
  {}

  lemma PhotosDeniedRootBlocks(configured: bool, allow: seq<string>, deny: seq<string>, path: string)
    requires path in deny && PhotoDenyPath(path)
    ensures !SynologyPhotosAllowed(configured, allow, deny)
  {}

  lemma PhotosDenyMonotonic(configured: bool, allow: seq<string>, oldDeny: seq<string>, newDeny: seq<string>)
    requires forall path <- oldDeny :: path in newDeny
    ensures !SynologyPhotosAllowed(configured, allow, oldDeny) ==>
      !SynologyPhotosAllowed(configured, allow, newDeny)
  {}

  // The protocol adapter computes each flag from its strict request validation.
  // This policy proves that none of those checks may be omitted at admission.
  function ValidOAuthRegistration(redirectCount: int, publicAuth: bool, grantsOK: bool,
                                 responseOK: bool, scopeOK: bool, redirectsOK: bool): bool
    ensures ValidOAuthRegistration(redirectCount, publicAuth, grantsOK, responseOK, scopeOK, redirectsOK) <==>
      (1 <= redirectCount <= 4 && publicAuth && grantsOK && responseOK && scopeOK && redirectsOK)
  {
    1 <= redirectCount <= 4 && publicAuth && grantsOK && responseOK && scopeOK && redirectsOK
  }

  lemma OAuthRegistrationRequiresEveryCheck(redirectCount: int, publicAuth: bool, grantsOK: bool,
                                          responseOK: bool, scopeOK: bool, redirectsOK: bool)
    requires ValidOAuthRegistration(redirectCount, publicAuth, grantsOK, responseOK, scopeOK, redirectsOK)
    ensures 1 <= redirectCount <= 4
    ensures publicAuth && grantsOK && responseOK && scopeOK && redirectsOK
  {}

  lemma OAuthRegistrationDenialMonotonic(redirectCount: int,
      oldAuth: bool, oldGrants: bool, oldResponse: bool, oldScope: bool, oldRedirects: bool,
      newAuth: bool, newGrants: bool, newResponse: bool, newScope: bool, newRedirects: bool)
    requires newAuth ==> oldAuth
    requires newGrants ==> oldGrants
    requires newResponse ==> oldResponse
    requires newScope ==> oldScope
    requires newRedirects ==> oldRedirects
    ensures !ValidOAuthRegistration(redirectCount, oldAuth, oldGrants, oldResponse, oldScope, oldRedirects) ==>
      !ValidOAuthRegistration(redirectCount, newAuth, newGrants, newResponse, newScope, newRedirects)
  {}

  function CanRegisterOAuthClient(existing: int, capacity: int): bool
    ensures CanRegisterOAuthClient(existing, capacity) <==>
      (existing >= 0 && 1 <= capacity <= 128 && existing < capacity)
  {
    existing >= 0 && 1 <= capacity <= 128 && existing < capacity
  }

  lemma OAuthRegistrationInsertWithinCapacity(existing: int, capacity: int)
    requires CanRegisterOAuthClient(existing, capacity)
    ensures 1 <= existing + 1 <= capacity <= 128
  {}

  lemma OAuthRegistrationFullCapacityRemainsDenied(existing: int, capacity: int, laterCount: int)
    requires 1 <= capacity <= 128 && capacity <= existing <= laterCount
    ensures !CanRegisterOAuthClient(existing, capacity)
    ensures !CanRegisterOAuthClient(laterCount, capacity)
  {}

  function FailedLoginCount(failures: int): int
    requires LoginAttemptAllowed(failures)
    ensures FailedLoginCount(failures) == failures + 1
    ensures 1 <= FailedLoginCount(failures) <= 5
  {
    failures + 1
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

  // Photos indexed with visual vectors carry a flavour suffix, so changing the photo
  // setting reindexes photos only and leaves documents and code untouched.
  const ClipFlavour: string := ";clip"

  function IndexedFingerprint(hash: string, clip: bool): string
    ensures clip ==> HasClipFlavour(IndexedFingerprint(hash, clip)) && ContentHash(IndexedFingerprint(hash, clip)) == hash
    ensures !clip ==> IndexedFingerprint(hash, clip) == hash
  {
    if clip then hash + ClipFlavour else hash
  }

  predicate HasClipFlavour(fingerprint: string) {
    |fingerprint| >= |ClipFlavour| && fingerprint[|fingerprint| - |ClipFlavour|..] == ClipFlavour
  }

  function ContentHash(fingerprint: string): string
    ensures !HasClipFlavour(fingerprint) ==> ContentHash(fingerprint) == fingerprint
    ensures HasClipFlavour(fingerprint) ==> ContentHash(fingerprint) + ClipFlavour == fingerprint
  {
    if HasClipFlavour(fingerprint) then fingerprint[..|fingerprint| - |ClipFlavour|] else fingerprint
  }

  // A stored file may be trusted without rereading it when its cheap stamp (size and
  // modification time) still matches and its fingerprint already has the flavour the
  // current photo setting would produce. An empty stamp predates stamps and never skips.
  function CanSkipUnchanged(storedStamp: string, currentStamp: string, storedFingerprint: string, clip: bool): bool
    ensures CanSkipUnchanged(storedStamp, currentStamp, storedFingerprint, clip) ==> storedStamp == currentStamp != ""
    // Skipping agrees with the hash path: hashing the same bytes would not replace the row.
    ensures CanSkipUnchanged(storedStamp, currentStamp, storedFingerprint, clip) ==>
      !ShouldReplace(storedFingerprint, IndexedFingerprint(ContentHash(storedFingerprint), clip))
  {
    storedStamp != "" && storedStamp == currentStamp &&
      IndexedFingerprint(ContentHash(storedFingerprint), clip) == storedFingerprint
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

  // ---- Shared memory and journal ----

  predicate ScopeChar(c: char) {
    ('a' <= c <= 'z') || ('0' <= c <= '9') || c == '-' || c == '_'
  }

  // Scopes are slash-separated lowercase segments such as "personal" or "work/acme".
  predicate ValidScope(s: string) {
    0 < |s| <= 128 && s[0] != '/' && s[|s|-1] != '/' &&
    (forall i | 0 <= i < |s| :: ScopeChar(s[i]) || s[i] == '/') &&
    (forall i | 0 <= i < |s| - 1 :: !(s[i] == '/' && s[i+1] == '/'))
  }

  // A scope lies within a root only at a segment boundary: "personal2" is not within "personal".
  predicate ScopeWithin(scope: string, root: string) {
    scope == root || (|root| < |scope| && scope[..|root|] == root && scope[|root|] == '/')
  }

  lemma ScopeWithinBoundary(scope: string, root: string)
    requires ScopeWithin(scope, root) && scope != root
    ensures |root| < |scope| && scope[|root|] == '/'
  {}

  lemma ScopeWithinTransitive(a: string, b: string, c: string)
    requires ScopeWithin(a, b) && ScopeWithin(b, c)
    ensures ScopeWithin(a, c)
  {
    if a != b && b != c {
      assert a[..|b|] == b;
      assert a[..|c|] == a[..|b|][..|c|];
      assert a[|c|] == a[..|b|][|c|] == b[|c|];
    }
  }

  // Retrieval is denied unless the entry's scope lies within one of the caller's roots.
  predicate ScopeVisible(scope: string, roots: seq<string>) {
    exists i | 0 <= i < |roots| :: ScopeWithin(scope, roots[i])
  }

  lemma NoRootsSeeNothing(scope: string)
    ensures !ScopeVisible(scope, [])
  {}

  predicate IsCurrentMemory(status: string) {
    status == "accepted"
  }

  predicate TerminalMemory(status: string) {
    status == "rejected" || status == "superseded" || status == "retracted"
  }

  predicate ValidMemoryTransition(from: string, to: string) {
    (from == "candidate" && (to == "accepted" || to == "rejected" || to == "disputed")) ||
    (from == "accepted" && (to == "superseded" || to == "retracted" || to == "disputed")) ||
    (from == "disputed" && (to == "accepted" || to == "rejected" || to == "retracted"))
  }

  lemma TerminalHasNoTransition(from: string, to: string)
    requires TerminalMemory(from)
    ensures !ValidMemoryTransition(from, to)
  {}

  ghost predicate ValidMemoryHistory(h: seq<string>) {
    forall i | 0 <= i < |h| - 1 :: ValidMemoryTransition(h[i], h[i+1])
  }

  // A retracted, superseded or rejected memory is the end of its history, so it can
  // never again be returned as current.
  lemma TerminalMemoryIsFinal(h: seq<string>, i: int)
    requires ValidMemoryHistory(h) && 0 <= i < |h| && TerminalMemory(h[i])
    ensures i == |h| - 1 && !IsCurrentMemory(h[|h| - 1])
  {
    if i < |h| - 1 {
      TerminalHasNoTransition(h[i], h[i+1]);
    }
  }

  // Assistants may only raise a dispute; accepting, rejecting and retracting are the owner's.
  predicate AssistantMayTransition(from: string, to: string) {
    ValidMemoryTransition(from, to) && to == "disputed"
  }

  lemma AssistantCannotMakeCurrent(from: string, to: string)
    requires AssistantMayTransition(from, to)
    ensures !IsCurrentMemory(to)
  {}

  // Entries leave the NAS for an external classifier only when one is configured, the
  // owner did not write them, and their scope is explicitly allowed out.
  predicate ExternalClassificationAllowed(configured: bool, userAttested: bool, scope: string, external: seq<string>) {
    configured && !userAttested && ScopeVisible(scope, external)
  }

  lemma OwnerEntriesStayLocal(configured: bool, scope: string, external: seq<string>)
    ensures !ExternalClassificationAllowed(configured, true, scope, external)
  {}

  predicate ValidMemoryCategory(c: string) {
    c in {"fact", "preference", "decision", "constraint", "lesson"}
  }

  // Only events that state something durable become memory candidates.
  predicate ProposesMemory(eventType: string) {
    eventType in {"decision", "observation", "correction", "manual_note"}
  }

  function DefaultMemoryCategory(eventType: string): string
    ensures ValidMemoryCategory(DefaultMemoryCategory(eventType))
  {
    if eventType == "decision" then "decision" else "fact"
  }

  predicate ValidEventType(t: string) {
    t in {"observation", "discussion", "exploration", "decision", "action",
          "result", "correction", "checkpoint", "manual_note"}
  }

  predicate ValidEventStatus(s: string) {
    s in {"proposed", "confirmed", "unverified", "superseded"}
  }

  // Optimistic concurrency: a checkpoint write succeeds only against the version it read.
  function CheckpointWrite(current: nat, expected: int): (bool, nat)
    ensures CheckpointWrite(current, expected).0 <==> expected == current
    ensures CheckpointWrite(current, expected).0 ==> CheckpointWrite(current, expected).1 == current + 1
    ensures !CheckpointWrite(current, expected).0 ==> CheckpointWrite(current, expected).1 == current
  {
    if expected == current then (true, current + 1) else (false, current)
  }

  function SumCosts(costs: seq<nat>, idx: seq<nat>): nat
    requires forall k | 0 <= k < |idx| :: idx[k] < |costs|
  {
    if |idx| == 0 then 0 else costs[idx[0]] + SumCosts(costs, idx[1..])
  }

  function TotalFrom(costs: seq<nat>, start: nat): nat
    requires start <= |costs|
    decreases |costs| - start
  {
    if start == |costs| then 0 else costs[start] + TotalFrom(costs, start + 1)
  }

  function PackFrom(costs: seq<nat>, remaining: nat, start: nat): seq<nat>
    requires start <= |costs|
    ensures forall k | 0 <= k < |PackFrom(costs, remaining, start)| ::
      start <= PackFrom(costs, remaining, start)[k] < |costs|
    ensures forall a, b | 0 <= a < b < |PackFrom(costs, remaining, start)| ::
      PackFrom(costs, remaining, start)[a] < PackFrom(costs, remaining, start)[b]
    ensures SumCosts(costs, PackFrom(costs, remaining, start)) <= remaining
    ensures TotalFrom(costs, start) <= remaining ==> |PackFrom(costs, remaining, start)| == |costs| - start
    decreases |costs| - start
  {
    if start == |costs| then []
    else if costs[start] <= remaining then
      var rest := PackFrom(costs, remaining - costs[start], start + 1);
      assert ([start] + rest)[1..] == rest;
      [start] + rest
    else PackFrom(costs, remaining, start + 1)
  }

  // Context packages keep items in priority order, skip any item that does not fit,
  // never exceed the budget, and include everything when everything fits.
  function PackBudget(costs: seq<nat>, budget: nat): seq<nat>
    ensures forall k | 0 <= k < |PackBudget(costs, budget)| :: PackBudget(costs, budget)[k] < |costs|
    ensures forall a, b | 0 <= a < b < |PackBudget(costs, budget)| ::
      PackBudget(costs, budget)[a] < PackBudget(costs, budget)[b]
    ensures SumCosts(costs, PackBudget(costs, budget)) <= budget
    ensures TotalFrom(costs, 0) <= budget ==> |PackBudget(costs, budget)| == |costs|
  {
    PackFrom(costs, budget, 0)
  }

  predicate ValidThresholds(acceptAt: int, rejectAt: int) {
    0 <= rejectAt < acceptAt <= 1000
  }

  // Probabilities are per-mille. A missing score or invalid thresholds go to human review.
  function RouteCandidate(p: int, acceptAt: int, rejectAt: int): string
    ensures RouteCandidate(p, acceptAt, rejectAt) in {"accept", "reject", "review"}
    ensures RouteCandidate(p, acceptAt, rejectAt) != "review" ==> 0 <= p <= 1000
    ensures RouteCandidate(p, acceptAt, rejectAt) == "accept" ==> ValidThresholds(acceptAt, rejectAt) && p >= acceptAt
    ensures RouteCandidate(p, acceptAt, rejectAt) == "reject" ==> ValidThresholds(acceptAt, rejectAt) && p <= rejectAt
  {
    if !ValidThresholds(acceptAt, rejectAt) || p < 0 || p > 1000 then "review"
    else if p >= acceptAt then "accept"
    else if p <= rejectAt then "reject"
    else "review"
  }

  lemma RouteCandidateMonotonic(p1: int, p2: int, acceptAt: int, rejectAt: int)
    requires 0 <= p1 <= p2 <= 1000
    ensures RouteCandidate(p1, acceptAt, rejectAt) == "accept" ==> RouteCandidate(p2, acceptAt, rejectAt) == "accept"
    ensures RouteCandidate(p2, acceptAt, rejectAt) == "reject" ==> RouteCandidate(p1, acceptAt, rejectAt) == "reject"
  {}

  // What a person writes or confirms is accepted; a model's proposal is accepted only
  // above the configured threshold.
  function InitialMemoryStatus(userAttested: bool, p: int, acceptAt: int, rejectAt: int): string
    ensures InitialMemoryStatus(userAttested, p, acceptAt, rejectAt) in {"accepted", "rejected", "candidate"}
    ensures !userAttested && InitialMemoryStatus(userAttested, p, acceptAt, rejectAt) == "accepted" ==>
      ValidThresholds(acceptAt, rejectAt) && p >= acceptAt
  {
    if userAttested then "accepted"
    else
      var route := RouteCandidate(p, acceptAt, rejectAt);
      if route == "accept" then "accepted" else if route == "reject" then "rejected" else "candidate"
  }

  // Fail closed: any mode other than "off", or "enroll" before the first passkey, requires one.
  predicate PasskeyRequired(mode: string, registered: int) {
    !(mode == "off" || (mode == "enroll" && registered <= 0))
  }

  lemma PasskeyEnrollmentIsOneWay(registered: int, later: int)
    requires registered <= later && PasskeyRequired("enroll", registered)
    ensures PasskeyRequired("enroll", later)
  {}

  predicate CanRegisterPasskey(registered: int, capacity: int) {
    registered >= 0 && 1 <= capacity <= 32 && registered < capacity
  }

  // WebAuthn clone detection: a counter must increase unless the authenticator never counts.
  predicate PasskeySignCountValid(stored: int, received: int) {
    stored >= 0 && received >= 0 && ((stored == 0 && received == 0) || received > stored)
  }

  predicate EditorAllowed(login: string, owner: string) {
    owner != "" && login == owner
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
