package download

import (
	"context"
	"sync"
)

// Fake is a Downloader for tests: it records what it was asked to add instead
// of talking to a real client.
//
// It lives here rather than in each test package because several packages need
// one, and a per-package copy would drift.
type Fake struct {
	mu sync.Mutex
	// Adds is every (magnet, dir) pair handed to Add, in order.
	Adds []FakeAdd
	// Err, when set, is what Add returns.
	Err error
}

// FakeAdd is one recorded Add call.
type FakeAdd struct {
	Magnet string
	Dir    string
}

func (f *Fake) Name() string { return "fake" }

func (f *Fake) Add(ctx context.Context, magnet, dir string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Adds = append(f.Adds, FakeAdd{Magnet: magnet, Dir: dir})
	return f.Err
}

// Last returns the most recent Add, or the zero value if there were none.
func (f *Fake) Last() FakeAdd {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.Adds) == 0 {
		return FakeAdd{}
	}
	return f.Adds[len(f.Adds)-1]
}

// Count is how many times Add was called.
func (f *Fake) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Adds)
}
