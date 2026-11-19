package vacuum

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/mattyv/nas-search-mcp/internal/policy"
)

// Place is a named point on the vacuum's map, kept on the NAS.
type Place struct {
	Name  string `json:"name"`
	Point [2]int `json:"point"`
}

// Places stores named map points in a small JSON file beside the index.
type Places struct {
	client *Client
	path   string
	mu     sync.Mutex
}

func NewPlaces(client *Client, path string) *Places {
	return &Places{client: client, path: path}
}

func (p *Places) load() ([]Place, error) {
	data, err := os.ReadFile(p.path)
	if errors.Is(err, os.ErrNotExist) {
		return []Place{}, nil
	}
	if err != nil {
		return nil, errors.New("could not read saved places")
	}
	var places []Place
	if err := json.Unmarshal(data, &places); err != nil {
		return nil, errors.New("saved places file is damaged")
	}
	return places, nil
}

func (p *Places) store(places []Place) error {
	sort.Slice(places, func(i, j int) bool { return places[i].Name < places[j].Name })
	data, err := json.MarshalIndent(places, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(p.path), ".vacuum-places-*")
	if err != nil {
		return errors.New("could not save places")
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(data); err == nil {
		err = temp.Chmod(0o600)
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp.Name(), p.path)
	}
	if err != nil {
		return errors.New("could not save places")
	}
	return nil
}

func names(places []Place) []string {
	out := make([]string, len(places))
	for i, place := range places {
		out[i] = place.Name
	}
	return out
}

// List returns saved places sorted by name.
func (p *Places) List() ([]Place, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.load()
}

// Save names a map point, or the vacuum's current position when point is nil.
// Saving an existing name moves it.
func (p *Places) Save(ctx context.Context, name string, point *[2]int) (Place, error) {
	if point == nil {
		status, err := p.client.Status(ctx)
		if err != nil {
			return Place{}, err
		}
		if status.Position == nil {
			return Place{}, fmt.Errorf("%w: the vacuum has not reported its position; give a point instead", ErrInput)
		}
		point = status.Position
	}
	rooms, err := p.client.Rooms(ctx)
	if err != nil {
		return Place{}, err
	}
	if rooms.Bounds == nil {
		return Place{}, ErrNoMap
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	places, err := p.load()
	if err != nil {
		return Place{}, err
	}
	if !policy.ValidVacuumPlace("save", name, names(places), point[0], point[1], *rooms.Bounds) {
		return Place{}, ErrInput
	}
	saved := Place{Name: name, Point: *point}
	kept := places[:0]
	for _, place := range places {
		if place.Name != name {
			kept = append(kept, place)
		}
	}
	if err := p.store(append(kept, saved)); err != nil {
		return Place{}, err
	}
	return saved, nil
}

// Forget removes a saved place.
func (p *Places) Forget(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	places, err := p.load()
	if err != nil {
		return err
	}
	if !policy.ValidVacuumPlace("forget", name, names(places), 0, 0, [4]int{}) {
		return ErrInput
	}
	kept := places[:0]
	for _, place := range places {
		if place.Name != name {
			kept = append(kept, place)
		}
	}
	return p.store(kept)
}

// Resolve returns a saved place's point.
func (p *Places) Resolve(name string) (*[2]int, error) {
	places, err := p.List()
	if err != nil {
		return nil, err
	}
	for _, place := range places {
		if place.Name == name {
			point := place.Point
			return &point, nil
		}
	}
	return nil, fmt.Errorf("%w: no saved place named %q", ErrInput, name)
}

// ResolveCommand replaces a command's place name with the saved point.
func (p *Places) ResolveCommand(in CommandInput) (CommandInput, error) {
	if in.Place == "" {
		return in, nil
	}
	if in.Point != nil {
		return in, fmt.Errorf("%w: give a place or a point, not both", ErrInput)
	}
	point, err := p.Resolve(in.Place)
	if err != nil {
		return in, err
	}
	in.Point, in.Place = point, ""
	return in, nil
}
