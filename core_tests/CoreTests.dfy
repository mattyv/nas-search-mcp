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
    expect MatchesTerms("week 10/notes [a].pdf", ["notes", "week 10"]);
    expect MatchesTerms("week 10/notes [a].pdf", ["[a].", ".pdf"]);
    expect !MatchesTerms("week 10/notes [a].pdf", ["notes", "absent"]);
    expect !MatchesTerms("week 10/notes [a].pdf", [".*"]);
    expect MatchesTerms("中文🎵/notes", ["🎵", "中文"]);
    expect MatchesTerms("", []);
    expect MatchesTerms("anything", [""]);
    expect !MatchesTerms("abc", ["abcd"]);
    expect ShowInline("application/pdf");
    expect ShowInline("image/jpeg");
    expect ShowInline("image/png");
    expect ShowInline("text/plain");
    expect !ShowInline("text/html");
    expect !ShowInline("image/svg+xml");
    expect !ShowInline("application/octet-stream");
    expect !ShowInline("");
    expect LinkActive(100, 101);
    expect !LinkActive(101, 101);
    expect !LinkActive(102, 101);
    expect !LoginAttemptAllowed(-1);
    expect LoginAttemptAllowed(0);
    expect LoginAttemptAllowed(4);
    expect !LoginAttemptAllowed(5);
    expect !LoginAttemptAllowed(6);
    expect FailedLoginCount(0) == 1;
    expect FailedLoginCount(4) == 5;
    expect ValidPhotoPage(0, 1);
    expect ValidPhotoPage(100000, 100);
    expect ValidPhotoPage(20, 20);
    expect !ValidPhotoPage(-1, 20);
    expect !ValidPhotoPage(100001, 20);
    expect !ValidPhotoPage(0, 0);
    expect !ValidPhotoPage(0, 101);
    expect ValidPhotoPerson(1);
    expect ValidPhotoPerson(3753);
    expect ValidPhotoPerson(2147483647);
    expect !ValidPhotoPerson(0);
    expect !ValidPhotoPerson(-1);
    expect !ValidPhotoPerson(2147483648);
    expect !SynologyPhotosAllowed(false, ["Photos"], []);
    expect !SynologyPhotosAllowed(true, [], []);
    expect !SynologyPhotosAllowed(true, ["Photos/Family"], []);
    expect !SynologyPhotosAllowed(true, ["Photos2"], []);
    expect SynologyPhotosAllowed(true, ["Photos"], []);
    expect SynologyPhotosAllowed(true, ["Photos", "Code"], ["Code", "Photos2"]);
    expect !SynologyPhotosAllowed(true, ["Photos"], ["Photos"]);
    expect !SynologyPhotosAllowed(true, ["Photos"], ["Photos/Private"]);
    expect !SynologyPhotosAllowed(true, ["Photos"], ["Photos/"]);
    print "Dafny core executable tests passed\n";
  }
}
