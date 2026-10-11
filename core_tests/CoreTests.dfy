include "../core/Core.dfy"

module CoreTests {
  import opened C = Core
  method Main() {
    expect !ValidRelative("");
    expect !ValidRelative("/etc/passwd");
    expect !ValidRelative("a/../b");
    expect !ValidRelative("a//b");
    expect !ValidRelative("a/./b");
    expect !ValidRelative("a\\b");
    expect !ValidRelative("a\0b");
    expect ValidRelative("docs/report.pdf");
    expect ValidRelative("a..b");
    expect ClampLimit(-3, 100) == 1;
    expect ClampLimit(200, 100) == 100;
    expect ClampLimit(7, 100) == 7;
    expect RankScore(0, 0) == 0;
    expect RankScore(1, 1) > RankScore(2, 2);
    expect !ShouldReplace("same", "same");
    expect ShouldReplace("old", "new");
    expect Chunk("", 4, 1) == [];
    expect Chunk("abcdef", 4, 1) == ["abcd", "def"];
    expect Chunk("abc", 4, 0) == ["abc"];
    expect Chunk("abcdefgh", 4, 0) == ["abcd", "efgh"];
    expect SortTop([(3, 10), (2, 20), (1, 20)], 2) == [(1, 20), (2, 20)];
    expect SortTop([(3, 10)], 0) == [];
    expect SortTop([(3, 10)], 5) == [(3, 10)];
    print "Dafny core executable tests passed\n";
  }
}
