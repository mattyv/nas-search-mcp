package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestListFoldersShowsOnlyAuthorizedLiveDirectories(t *testing.T) {
	a := accessApp(t)
	for _, folder := range []string{
		"Documents/Trips/2024", "Documents/Taxes", "Documents/.hidden",
		"Documents/node_modules", "Code/Public", "Code/Private",
	} {
		if err := os.MkdirAll(filepath.Join(a.Root, folder), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(a.Root, "Documents/loose.txt"), "not a directory")
	if err := os.Symlink(filepath.Join(a.Root, "Code/Private"), filepath.Join(a.Root, "Documents/shortcut")); err != nil {
		t.Fatal(err)
	}
	if err := a.ConfigureAccess(nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ListFolders(context.Background(), "", 20); err == nil {
		t.Fatal("empty allowlist exposed root folders")
	}
	if err := a.ConfigureAccess([]string{"Documents/Trips/2024", "Code/Public"}, []string{"Documents/Taxes"}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path string
		want []string
	}{
		{"", []string{"Code", "Documents"}},
		{"Documents/", []string{"Documents/Trips"}},
		{"Documents/Trips", []string{"Documents/Trips/2024"}},
		{"Documents/Trips/2024", []string{}},
		{"Code", []string{"Code/Public"}},
	} {
		got, err := a.ListFolders(context.Background(), test.path, 20)
		if err != nil || !reflect.DeepEqual(got.Folders, test.want) || got.Truncated {
			t.Fatalf("ListFolders(%q) = %+v, %v; want %v", test.path, got, err, test.want)
		}
	}
	for _, path := range []string{"/", "../", "Documents/../Code", "Documents/shortcut", "Documents/Taxes", "Code/Private", "Documents/loose.txt"} {
		if _, err := a.ListFolders(context.Background(), path, 20); err == nil {
			t.Fatal("listed invalid or unauthorized folder", path)
		}
	}
}

func TestListFoldersBoundsResultsAndHonorsCancellation(t *testing.T) {
	a := accessApp(t)
	for _, folder := range []string{"Documents/A", "Documents/B", "Documents/C"} {
		if err := os.MkdirAll(filepath.Join(a.Root, folder), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.ConfigureAccess([]string{"Documents"}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := a.ListFolders(context.Background(), "Documents", 2)
	if err != nil || len(got.Folders) != 2 || !got.Truncated {
		t.Fatalf("bounded listing = %+v, %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.ListFolders(ctx, "Documents", 20); err != context.Canceled {
		t.Fatalf("cancelled listing error = %v", err)
	}
}
