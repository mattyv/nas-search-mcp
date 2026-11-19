package vacuum

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/mattyv/nas-search-mcp/internal/policy"
)

func placesBroker(t *testing.T, position string) *Client {
	t.Helper()
	socket := broker(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			w.Write([]byte(`{"name":"Roberto","connected":true,"state":"idle","battery":80,"docked":false,"error":"","suction":"","current_room":"Hall","cleaned_area_m2":0,"cleaning_minutes":0` + position + `}`))
		case "/rooms":
			w.Write([]byte(`{"rooms":[],"bounds":[0,0,1000,1000]}`))
		}
	})
	return newClient(t, socket)
}

func TestPlacesSaveListForgetAndPersist(t *testing.T) {
	ctx := context.Background()
	client := placesBroker(t, `,"position":[120,340]`)
	path := filepath.Join(t.TempDir(), "vacuum-places.json")
	places := NewPlaces(client, path)

	here, err := places.Save(ctx, "Front door", nil)
	if err != nil || here.Point != [2]int{120, 340} {
		t.Fatalf("save here = %#v, %v", here, err)
	}
	if _, err := places.Save(ctx, "Couch", &[2]int{500, 600}); err != nil {
		t.Fatal(err)
	}
	if _, err := places.Save(ctx, "Couch", &[2]int{510, 610}); err != nil {
		t.Fatal("re-saving a name should replace it:", err)
	}
	if _, err := places.Save(ctx, "Outside", &[2]int{5000, 10}); !errors.Is(err, ErrInput) {
		t.Fatalf("off-map point: %v", err)
	}
	if _, err := places.Save(ctx, "", &[2]int{1, 1}); !errors.Is(err, ErrInput) {
		t.Fatalf("empty name: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("places file: %v %v", info, err)
	}

	reopened := NewPlaces(client, path)
	list, err := reopened.List()
	if err != nil || len(list) != 2 || list[0].Name != "Couch" || list[0].Point != [2]int{510, 610} || list[1].Name != "Front door" {
		t.Fatalf("list = %#v, %v", list, err)
	}
	point, err := reopened.Resolve("Front door")
	if err != nil || *point != [2]int{120, 340} {
		t.Fatalf("resolve = %v, %v", point, err)
	}
	if _, err := reopened.Resolve("Garage"); !errors.Is(err, ErrInput) {
		t.Fatalf("unknown place: %v", err)
	}
	if err := reopened.Forget("Couch"); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Forget("Couch"); !errors.Is(err, ErrInput) {
		t.Fatalf("forgetting twice: %v", err)
	}
	if list, _ := reopened.List(); len(list) != 1 {
		t.Fatalf("after forget: %#v", list)
	}
}

func TestPlacesNeedPositionToSaveHere(t *testing.T) {
	places := NewPlaces(placesBroker(t, ""), filepath.Join(t.TempDir(), "p.json"))
	if _, err := places.Save(context.Background(), "Here", nil); !errors.Is(err, ErrInput) {
		t.Fatalf("no position known: %v", err)
	}
}

func TestPlacesLimit(t *testing.T) {
	ctx := context.Background()
	places := NewPlaces(placesBroker(t, ""), filepath.Join(t.TempDir(), "p.json"))
	for i := 0; i < policy.VacuumMaxPlaces; i++ {
		if _, err := places.Save(ctx, string(rune('A'+i)), &[2]int{1, 1}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := places.Save(ctx, "one too many", &[2]int{1, 1}); !errors.Is(err, ErrInput) {
		t.Fatalf("over the limit: %v", err)
	}
}

func TestStatusReportsPosition(t *testing.T) {
	status, err := placesBroker(t, `,"position":[7,8]`).Status(context.Background())
	if err != nil || status.Position == nil || *status.Position != [2]int{7, 8} {
		t.Fatalf("status = %#v, %v", status, err)
	}
}
