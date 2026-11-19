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

  predicate ValidMemoryStatus(status: string) {
    status in {"candidate", "accepted", "disputed", "rejected", "superseded", "retracted"}
  }

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

  // Recording activity never implies a request to create a lasting memory.
  predicate ProposesMemory(eventType: string) {
    eventType == "memory"
  }

  lemma RoutineJournalNeedsNoReview(eventType: string)
    requires eventType != "memory"
    ensures !ProposesMemory(eventType)
  {}

  function MemorySaveOutcome(eventType: string, created: bool): string
    ensures MemorySaveOutcome(eventType, created) == "memory_suggestion" <==> created && ProposesMemory(eventType)
    ensures MemorySaveOutcome(eventType, created) == "already_saved" <==> !created
    ensures MemorySaveOutcome(eventType, created) == "journal_only" <==> created && !ProposesMemory(eventType)
  {
    if !created then "already_saved"
    else if ProposesMemory(eventType) then "memory_suggestion"
    else "journal_only"
  }

  function DefaultMemoryCategory(eventType: string): string
    ensures ValidMemoryCategory(DefaultMemoryCategory(eventType))
  {
    if eventType == "decision" then "decision" else "fact"
  }

  predicate ValidEventType(t: string) {
    t in {"observation", "discussion", "exploration", "decision", "action",
          "result", "correction", "checkpoint", "manual_note", "memory"}
  }

  predicate ValidEventStatus(s: string) {
    s in {"proposed", "confirmed", "unverified", "superseded"}
  }

  // Assistants may record any journal type except checkpoints, which only the
  // checkpoint operation writes.
  predicate AssistantMayAppendType(t: string) {
    ValidEventType(t) && t != "checkpoint"
  }

  // Superseding marks the older event "superseded". An assistant may not do that to an
  // entry the owner confirmed; the link is kept but the owner's status stands.
  predicate MaySupersede(newActorIsUser: bool, oldStatus: string) {
    newActorIsUser || oldStatus != "confirmed"
  }

  lemma AssistantsKeepOwnerConfirmations(oldStatus: string)
    requires oldStatus == "confirmed"
    ensures !MaySupersede(false, oldStatus)
  {}

  // Callers of the private socket are never the owner, so it never forgets.
  predicate SocketMethodAllowed(name: string) {
    name != "ForgetEvent" && name != "ForgetMemory"
  }

  // A correction carries a replacement statement only when it supersedes.
  predicate CorrectionMayCarryStatement(to: string) {
    to == "superseded"
  }

  // Project-less memories that apply across a scope in every context package.
  predicate ScopeWideContextCategory(category: string) {
    category == "preference" || category == "constraint"
  }

  // The status an event gets when its writer leaves it empty.
  function DefaultEventStatus(actorIsUser: bool, eventType: string): string
    ensures ValidEventStatus(DefaultEventStatus(actorIsUser, eventType))
    ensures !actorIsUser ==> DefaultEventStatus(actorIsUser, eventType) != "confirmed"
  {
    if actorIsUser then "confirmed" else if eventType == "exploration" then "proposed" else "unverified"
  }

  // Only the owner may confirm; assistants may write any other valid status.
  predicate EventStatusAllowed(actorIsUser: bool, status: string) {
    ValidEventStatus(status) && (actorIsUser || status != "confirmed")
  }

  // Whatever an assistant writes, explicitly or by default, is never confirmed.
  lemma AssistantNeverConfirms(eventType: string, explicit: string)
    ensures DefaultEventStatus(false, eventType) != "confirmed"
    ensures EventStatusAllowed(false, explicit) ==> explicit != "confirmed"
  {}

  predicate IsUnconfirmedMemory(status: string) {
    status == "candidate" || status == "disputed"
  }

  lemma UnconfirmedIsNeverCurrent(status: string)
    requires IsUnconfirmedMemory(status)
    ensures !IsCurrentMemory(status)
  {}

  function ContextBudget(requested: int): int
    ensures 200 <= ContextBudget(requested) <= 8000
  {
    if requested <= 0 then 1500 else if requested < 200 then 200 else if requested > 8000 then 8000 else requested
  }

  // A memory is erased with a forgotten event only when that event was its sole evidence.
  predicate OrphanedAfterForget(evidence: seq<string>, forgotten: string) {
    |evidence| > 0 && forall i | 0 <= i < |evidence| :: evidence[i] == forgotten
  }

  lemma OtherEvidenceKeepsMemory(evidence: seq<string>, forgotten: string, i: int)
    requires 0 <= i < |evidence| && evidence[i] != forgotten
    ensures !OrphanedAfterForget(evidence, forgotten)
  {}

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
    ensures start < |costs| && costs[start] <= remaining ==>
      |PackFrom(costs, remaining, start)| > 0 && PackFrom(costs, remaining, start)[0] == start
    ensures start < |costs| && costs[start] > remaining ==>
      PackFrom(costs, remaining, start) == PackFrom(costs, remaining, start + 1)
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
    // The highest-priority item (the checkpoint) is always kept when it fits on its own.
    ensures |costs| > 0 && costs[0] <= budget ==> |PackBudget(costs, budget)| > 0 && PackBudget(costs, budget)[0] == 0
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

  // Passkeys need a host name, so an IP-address issuer defaults to off.
  function DefaultPasskeyMode(issuerHostIsIP: bool): string
    ensures ValidPasskeyMode(DefaultPasskeyMode(issuerHostIsIP))
  {
    if issuerHostIsIP then "off" else "enroll"
  }

  predicate ValidPasskeyMode(mode: string) {
    mode in {"off", "enroll", "required"}
  }

  // What follows a correct password: finish, prove a registered passkey, or enroll one.
  function PasskeyStep(mode: string, registered: int): string
    ensures PasskeyStep(mode, registered) in {"complete", "assert", "enroll_optional", "enroll_required"}
    ensures PasskeyStep(mode, registered) in {"complete", "enroll_optional"} <==> !PasskeyRequired(mode, registered)
    ensures PasskeyRequired(mode, registered) && registered > 0 ==> PasskeyStep(mode, registered) == "assert"
  {
    if !PasskeyRequired(mode, registered) then (if mode == "enroll" then "enroll_optional" else "complete")
    else if registered <= 0 then "enroll_required"
    else "assert"
  }

  // Once a passkey exists in enroll or required mode, a password alone never completes sign-in.
  lemma RegisteredPasskeyIsAlwaysAsked(mode: string, registered: int)
    requires mode != "off" && registered > 0
    ensures PasskeyStep(mode, registered) == "assert"
  {}

  // Required mode never removes its own last passkey.
  predicate PasskeyRemovalAllowed(mode: string, registered: int) {
    registered >= 1 && !(mode == "required" && registered <= 1)
  }

  lemma RequiredModeKeepsOnePasskey(registered: int)
    requires PasskeyRemovalAllowed("required", registered)
    ensures registered - 1 >= 1
  {}

  // An owner invite (made over SSH on the NAS) stands in for a passkey only to register another one.
  function PasskeyStepInvited(mode: string, registered: int, invited: bool): string
    ensures !invited ==> PasskeyStepInvited(mode, registered, invited) == PasskeyStep(mode, registered)
    ensures invited && PasskeyStep(mode, registered) == "assert" ==> PasskeyStepInvited(mode, registered, invited) == "enroll_required"
    ensures PasskeyStepInvited(mode, registered, invited) in {"complete", "assert", "enroll_optional", "enroll_required"}
  {
    if invited && PasskeyStep(mode, registered) == "assert" then "enroll_required" else PasskeyStep(mode, registered)
  }

  // An invite never finishes a sign-in on its own: where a passkey is required, the
  // invited step still needs a passkey ceremony and offers no skip.
  lemma InviteNeverSkipsThePasskey(mode: string, registered: int, invited: bool)
    requires PasskeyRequired(mode, registered)
    ensures PasskeyStepInvited(mode, registered, invited) in {"assert", "enroll_required"}
  {}

  predicate PasskeyInviteUsable(used: bool, now: int, expires: int) {
    !used && now < expires
  }

  predicate ValidPasskeyInviteMinutes(minutes: int) {
    1 <= minutes <= 60
  }

  predicate CanRegisterPasskey(registered: int, capacity: int) {
    registered >= 0 && 1 <= capacity <= 32 && registered < capacity
  }

  // WebAuthn clone detection: a counter must increase unless the authenticator never counts.
  predicate PasskeySignCountValid(stored: int, received: int) {
    stored >= 0 && received >= 0 && ((stored == 0 && received == 0) || received > stored)
  }

  // Memory backups: keep between 1 and 90 copies.
  predicate ValidBackupRetention(keep: int) {
    1 <= keep <= 90
  }

  // A backup is kept when it is among the newest `keep` copies (rank 0 is the newest).
  predicate KeepBackup(rankNewestFirst: int, keep: int) {
    ValidBackupRetention(keep) && 0 <= rankNewestFirst < keep
  }

  // Pruning never removes the newest copy, so a successful backup always survives.
  lemma NewestBackupIsKept(keep: int)
    requires ValidBackupRetention(keep)
    ensures KeepBackup(0, keep)
  {}

  lemma OlderBackupsGoFirst(newer: int, older: int, keep: int)
    requires 0 <= newer <= older && KeepBackup(older, keep)
    ensures KeepBackup(newer, keep)
  {}

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

  // Robot vacuum control. The adapter forwards only these commands to the
  // vacuum broker; everything else the vendor library can do stays unreachable.
  const VacuumSimpleActions: seq<string> := ["start", "pause", "stop", "dock", "locate",
    "empty_dustbin", "wash_mops", "dry_mops", "stop_drying"]
  const VacuumActions: seq<string> := VacuumSimpleActions + ["clean_rooms", "clean_zones", "clean_spot", "go_to"]
  const MaxVacuumRooms: nat := 32
  const MaxVacuumZones: nat := 5
  // Smallest zone side, in map millimetres; smaller zones are mis-aimed points.
  const MinVacuumZoneSide: nat := 300

  function VacuumAction(action: string): bool
    ensures VacuumAction(action) <==> action in VacuumActions
  {
    action in VacuumActions
  }

  function VacuumNeedsRooms(action: string): bool
    ensures VacuumNeedsRooms(action) <==> action == "clean_rooms"
  {
    action == "clean_rooms"
  }

  function VacuumNeedsMap(action: string): bool
    ensures VacuumNeedsMap(action) <==> action == "clean_zones" || action == "clean_spot" || action == "go_to"
  {
    action == "clean_zones" || action == "clean_spot" || action == "go_to"
  }

  predicate VacuumPointAction(action: string) {
    action == "clean_spot" || action == "go_to"
  }

  predicate DistinctInts(xs: seq<int>) {
    forall i, j | 0 <= i < j < |xs| :: xs[i] != xs[j]
  }

  predicate ValidPassesAndSuction(repeats: int, suction: int) {
    1 <= repeats <= 3 && -1 <= suction <= 3
  }

  // Simple commands carry no room, pass, or suction arguments. Room cleaning
  // names only rooms the vacuum reported, each once, with 1-3 passes and
  // suction -1 (keep current) or 0-3 (quiet..turbo). Zone and spot cleaning are
  // checked by ValidVacuumZones and ValidVacuumSpot instead.
  function ValidVacuumCommand(action: string, rooms: seq<int>, known: seq<int>, repeats: int, suction: int): bool
    ensures ValidVacuumCommand(action, rooms, known, repeats, suction) ==> VacuumAction(action) && !VacuumNeedsMap(action)
    ensures ValidVacuumCommand(action, rooms, known, repeats, suction) && VacuumNeedsRooms(action) ==>
      1 <= |rooms| <= MaxVacuumRooms && DistinctInts(rooms) && (forall r <- rooms :: r in known) &&
      ValidPassesAndSuction(repeats, suction)
    ensures ValidVacuumCommand(action, rooms, known, repeats, suction) && !VacuumNeedsRooms(action) ==>
      action in VacuumSimpleActions && |rooms| == 0 && repeats == 0 && suction == -1
  {
    if VacuumNeedsRooms(action) then
      1 <= |rooms| <= MaxVacuumRooms && DistinctInts(rooms) && (forall r <- rooms :: r in known) &&
      ValidPassesAndSuction(repeats, suction)
    else
      action in VacuumSimpleActions && |rooms| == 0 && repeats == 0 && suction == -1
  }

  // Each action carries only its own kind of target: rooms, zones, or a point.
  function VacuumTargetsMatch(action: string, rooms: nat, zones: nat, point: bool): bool
    ensures VacuumTargetsMatch(action, rooms, zones, point) && !VacuumNeedsMap(action) ==> zones == 0 && !point
    ensures VacuumTargetsMatch(action, rooms, zones, point) && VacuumNeedsMap(action) ==> rooms == 0
  {
    (action == "clean_zones" ==> rooms == 0 && !point) &&
    (VacuumPointAction(action) ==> rooms == 0 && zones == 0 && point) &&
    (action != "clean_zones" ==> zones == 0) &&
    (!VacuumPointAction(action) ==> !point)
  }

  // A zone is (x0, y0, x1, y1); bounds is the map's extent in the same units.
  predicate ZoneInside(zone: (int, int, int, int), bounds: (int, int, int, int)) {
    bounds.0 <= zone.0 && zone.0 + MinVacuumZoneSide <= zone.2 && zone.2 <= bounds.2 &&
    bounds.1 <= zone.1 && zone.1 + MinVacuumZoneSide <= zone.3 && zone.3 <= bounds.3
  }

  function ValidVacuumZones(zones: seq<(int, int, int, int)>, bounds: (int, int, int, int), repeats: int, suction: int): bool
    ensures ValidVacuumZones(zones, bounds, repeats, suction) ==>
      1 <= |zones| <= MaxVacuumZones && (forall z <- zones :: ZoneInside(z, bounds)) &&
      ValidPassesAndSuction(repeats, suction)
  {
    1 <= |zones| <= MaxVacuumZones && (forall z <- zones :: ZoneInside(z, bounds)) &&
    ValidPassesAndSuction(repeats, suction)
  }

  function ValidVacuumSpot(x: int, y: int, bounds: (int, int, int, int), repeats: int, suction: int): bool
    ensures ValidVacuumSpot(x, y, bounds, repeats, suction) ==>
      bounds.0 <= x <= bounds.2 && bounds.1 <= y <= bounds.3 && ValidPassesAndSuction(repeats, suction)
  {
    bounds.0 <= x <= bounds.2 && bounds.1 <= y <= bounds.3 && ValidPassesAndSuction(repeats, suction)
  }

  // Driving to a point carries no passes or suction; the point must be on the saved map.
  function ValidVacuumGoTo(x: int, y: int, bounds: (int, int, int, int), repeats: int, suction: int): bool
    ensures ValidVacuumGoTo(x, y, bounds, repeats, suction) ==>
      bounds.0 <= x <= bounds.2 && bounds.1 <= y <= bounds.3 && repeats == 0 && suction == -1
  {
    bounds.0 <= x <= bounds.2 && bounds.1 <= y <= bounds.3 && repeats == 0 && suction == -1
  }

  // One remote-control step: turn in degrees and speed within the vendor's
  // limits. A step that neither turns nor moves is meaningless and refused.
  function ValidVacuumMove(rotation: int, velocity: int): bool
    ensures ValidVacuumMove(rotation, velocity) ==>
      -360 <= rotation <= 360 && -600 <= velocity <= 600 && (rotation != 0 || velocity != 0)
  {
    -360 <= rotation <= 360 && -600 <= velocity <= 600 && (rotation != 0 || velocity != 0)
  }

  // Obstacle photos are fetched only for obstacles the vacuum itself reported.
  function ValidVacuumObstacle(index: int, known: seq<int>): bool
    ensures ValidVacuumObstacle(index, known) ==> index > 0 && index in known
  {
    index > 0 && index in known
  }

  lemma VacuumGoToNeverLeavesMap(x: int, y: int, bounds: (int, int, int, int), repeats: int, suction: int)
    requires x < bounds.0 || x > bounds.2 || y < bounds.1 || y > bounds.3
    ensures !ValidVacuumGoTo(x, y, bounds, repeats, suction)
  {}

  lemma VacuumUnknownObstacleRejected(index: int, known: seq<int>)
    requires index !in known
    ensures !ValidVacuumObstacle(index, known)
  {}

  // Generic vacuum settings. The vacuum reports each setting it has as
  // (name, lowest, highest, step, allowed values). A change is forwarded only
  // for a reported setting: one of its allowed values when it has a list, or
  // a value on its step grid within its range otherwise.
  predicate OptionAccepts(o: (string, int, int, int, seq<int>), value: int) {
    if |o.4| > 0 then value in o.4
    else o.3 > 0 && o.1 <= value <= o.2 && (value - o.1) % o.3 == 0
  }

  function ValidVacuumOption(name: string, value: int, options: seq<(string, int, int, int, seq<int>)>): bool
    ensures ValidVacuumOption(name, value, options) ==>
      exists o :: o in options && o.0 == name && OptionAccepts(o, value)
  {
    exists o | o in options :: o.0 == name && OptionAccepts(o, value)
  }

  lemma VacuumUnreportedOptionRejected(name: string, value: int, options: seq<(string, int, int, int, seq<int>)>)
    requires forall o | o in options :: o.0 != name
    ensures !ValidVacuumOption(name, value, options)
  {}

  // App shortcuts: start, rename, or delete only a shortcut the vacuum listed.
  // Renames carry a 1-32 character name; other actions carry none.
  function ValidVacuumShortcut(action: string, id: int, known: seq<int>, nameLength: int): bool
    ensures ValidVacuumShortcut(action, id, known, nameLength) ==>
      action in ["start", "rename", "delete"] && id in known &&
      (if action == "rename" then 1 <= nameLength <= 32 else nameLength == 0)
  {
    action in ["start", "rename", "delete"] && id in known &&
    (if action == "rename" then 1 <= nameLength <= 32 else nameLength == 0)
  }

  lemma VacuumUnknownShortcutRejected(action: string, id: int, known: seq<int>, nameLength: int)
    requires id !in known
    ensures !ValidVacuumShortcut(action, id, known, nameLength)
  {}

  // Settings the assistant may change, each with its allowed integer range.
  // Enumerated values are indexes the adapter maps from names.
  datatype SettingRange = NoSetting | Range(lo: int, hi: int, perRoom: bool)

  function VacuumSettingRange(name: string): SettingRange {
    match name
    case "cleaning_mode" => Range(0, 3, false)
    case "suction" => Range(0, 3, false)
    case "mop_wetness" => Range(0, 2, false)
    case "volume" => Range(0, 100, false)
    case "dnd" => Range(0, 1, false)
    case "dnd_start" => Range(0, 1439, false)
    case "dnd_end" => Range(0, 1439, false)
    case "child_lock" => Range(0, 1, false)
    case "carpet_avoidance" => Range(0, 1, false)
    case "carpet_recognition" => Range(0, 1, false)
    case "room_mode" => Range(0, 3, true)
    case "room_suction" => Range(0, 3, true)
    case "room_wetness" => Range(0, 2, true)
    case "room_passes" => Range(1, 3, true)
    case _ => NoSetting
  }

  function VacuumSettingNeedsRoom(name: string): bool {
    VacuumSettingRange(name).Range? && VacuumSettingRange(name).perRoom
  }

  // Whole-vacuum settings use room 0; per-room settings name a reported room.
  function ValidVacuumSetting(name: string, value: int, room: int, known: seq<int>): bool
    ensures ValidVacuumSetting(name, value, room, known) ==>
      VacuumSettingRange(name).Range? &&
      VacuumSettingRange(name).lo <= value <= VacuumSettingRange(name).hi
    ensures ValidVacuumSetting(name, value, room, known) && VacuumSettingNeedsRoom(name) ==> room in known && room > 0
    ensures ValidVacuumSetting(name, value, room, known) && !VacuumSettingNeedsRoom(name) ==> room == 0
  {
    match VacuumSettingRange(name)
    case NoSetting => false
    case Range(lo, hi, perRoom) =>
      lo <= value <= hi && (if perRoom then room > 0 && room in known else room == 0)
  }

  function ValidVacuumHistoryLimit(limit: int): bool
    ensures ValidVacuumHistoryLimit(limit) <==> 1 <= limit <= 20
  {
    1 <= limit <= 20
  }

  lemma VacuumNeverCleansUnknownRoom(action: string, rooms: seq<int>, known: seq<int>, repeats: int, suction: int, room: int)
    requires room in rooms && room !in known
    ensures !ValidVacuumCommand(action, rooms, known, repeats, suction)
  {}

  lemma VacuumUnknownActionRejected(action: string, rooms: seq<int>, known: seq<int>, repeats: int, suction: int)
    requires action !in VacuumActions
    ensures !ValidVacuumCommand(action, rooms, known, repeats, suction)
  {}

  lemma VacuumNoRoomsKnownNoRoomCleaning(rooms: seq<int>, repeats: int, suction: int)
    ensures !ValidVacuumCommand("clean_rooms", rooms, [], repeats, suction)
  {
    if ValidVacuumCommand("clean_rooms", rooms, [], repeats, suction) {
      assert rooms[0] in rooms;
    }
  }

  lemma VacuumZoneNeverLeavesMap(zones: seq<(int, int, int, int)>, bounds: (int, int, int, int), repeats: int, suction: int, z: (int, int, int, int))
    requires z in zones && !ZoneInside(z, bounds)
    ensures !ValidVacuumZones(zones, bounds, repeats, suction)
  {}

  lemma VacuumUnknownSettingRejected(name: string, value: int, room: int, known: seq<int>)
    requires VacuumSettingRange(name) == NoSetting
    ensures !ValidVacuumSetting(name, value, room, known)
  {}

  lemma VacuumRoomSettingNeverTargetsUnknownRoom(name: string, value: int, room: int, known: seq<int>)
    requires VacuumSettingNeedsRoom(name) && room !in known
    ensures !ValidVacuumSetting(name, value, room, known)
  {}
}
