package scoring

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"asmgraph/engine"
)

// Entry is the personal best for one dimension.
type Entry struct {
	Score engine.Score `json:"score"`
	// File is the saved solution, relative to the puzzle's best directory.
	File     string    `json:"file"`
	Achieved time.Time `json:"achieved"`
}

// Record holds a puzzle's personal bests.
type Record struct {
	Puzzle string               `json:"puzzle"`
	Bests  map[Dimension]*Entry `json:"bests"`
}

// Store keeps personal bests on disk:
//
//	<Root>/<puzzle>/bests/scores.json
//	<Root>/<puzzle>/bests/best-cost.yaml   (and best-ticks, best-bytes)
//
// Puzzle ids must already be validated by the caller (they are used as
// directory names). The store is not safe for concurrent use; callers
// serialise access.
type Store struct {
	Root string
	// Now returns the current time (for tests).
	Now func() time.Time
}

func (s *Store) dir(puzzle string) string { return filepath.Join(s.Root, puzzle, "bests") }

// Load returns the puzzle's record (empty if none was saved yet).
func (s *Store) Load(puzzle string) (*Record, error) {
	r := &Record{Puzzle: puzzle, Bests: map[Dimension]*Entry{}}
	data, err := os.ReadFile(filepath.Join(s.dir(puzzle), "scores.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, fmt.Errorf("corrupt personal-best record: %w", err)
	}
	if r.Bests == nil {
		r.Bests = map[Dimension]*Entry{}
	}
	r.Puzzle = puzzle
	return r, nil
}

// Submit records a passing solution's score. For every dimension where it
// beats the current personal best (see Better) it saves the solution as
// that dimension's best. It returns the updated record and the improved
// dimensions.
func (s *Store) Submit(puzzle string, score engine.Score, solutionYAML []byte) (*Record, []Dimension, error) {
	r, err := s.Load(puzzle)
	if err != nil {
		return nil, nil, err
	}
	var improved []Dimension
	for _, d := range Dimensions {
		if cur := r.Bests[d]; cur == nil || Better(score, cur.Score, d) {
			improved = append(improved, d)
		}
	}
	if len(improved) == 0 {
		return r, nil, nil
	}
	dir := s.dir(puzzle)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	for _, d := range improved {
		file := "best-" + string(d) + ".yaml"
		if err := writeAtomic(filepath.Join(dir, file), solutionYAML); err != nil {
			return nil, nil, err
		}
		r.Bests[d] = &Entry{Score: score, File: file, Achieved: now().UTC()}
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	if err := writeAtomic(filepath.Join(dir, "scores.json"), data); err != nil {
		return nil, nil, err
	}
	return r, improved, nil
}

// Solution returns the saved solution YAML for a dimension's best.
func (s *Store) Solution(puzzle string, d Dimension) ([]byte, error) {
	r, err := s.Load(puzzle)
	if err != nil {
		return nil, err
	}
	e := r.Bests[d]
	if e == nil {
		return nil, fs.ErrNotExist
	}
	return os.ReadFile(filepath.Join(s.dir(puzzle), filepath.Base(e.File)))
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
