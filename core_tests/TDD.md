The executable tests were written before Core.dfy. Initial command:

    .tools/dafny/dafny verify core_tests/CoreTests.dfy

failed because the implementation file did not exist (red).

After implementation:

    .tools/dafny/dafny verify core/Core.dfy core_tests/CoreTests.dfy

passes with 30 verified obligations and zero errors (green).

The build script verifies both files explicitly, executes expectations through
the Go backend, and regenerates production Go source. Tests exercise path
rejection, limits, ranking, change detection, empty/final/overlapping chunks.

ChunkCoverage proves exact reconstruction after removing repeated overlap.
Recursive chunking terminates because every step advances size - overlap > 0.
Each returned chunk is nonempty and bounded by size. ClampLimit proves its
result lies in 1..maximum. Map lemmas prove replacement/removal idempotence and
preservation of unrelated keys. All proofs use bodies; no assume, axioms, or
disabled verification are used.

These are mathematical policy proofs, not filesystem or database proofs. The
adapter must validate preconditions, resolve symlinks, enforce share boundaries,
perform authorization, and atomically apply index changes. Model relevance and
extraction correctness remain outside these proof claims.

Ranking selection extension was also test-first. SortTop expectations initially
failed with three unresolved-identifier errors. The implemented selection sorts
(ID, score) pairs by descending score, then ascending ID, and takes the requested
prefix. Sort proves exact multiset permutation. SortTop proves sortedness,
length min(input length, limit), multiset containment, ID uniqueness when input
IDs are unique, and that every selected pair precedes every excluded pair.
The RRF monotonic lemma proves better positive ranks cannot lower the fixed-point
score, using Euclidean quotient/remainder arithmetic. The lexical path lemma
states only lexical confinement, not resolved filesystem confinement.

Insertion sorting is quadratic and intended for bounded retrieval candidates.
The runtime adapters must bound candidate counts and validate nonnegative limits.
