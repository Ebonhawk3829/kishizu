package adapt

import (
	"sync"

	"github.com/Ebonhawk3829/kishizu/internal/release"
	"github.com/Ebonhawk3829/kishizu/internal/store"
)

// Vocab is a cached view of the learned vocabulary.
//
// The vocabulary is global — it is not per show — but it was loaded from
// SQLite once per show per poll, because the adapter that needed it was built
// per show. At a 3-minute poll with a handful of shows that is a full table
// scan every tick, for data that changes only when the user teaches it
// something.
//
// Caching it here means one load until something is learned. Refresh is
// explicit rather than time-based: a TTL would either re-read constantly or
// let a just-learned token go unhonoured, and the writer knows when it wrote.
type Vocab struct {
	mu    sync.RWMutex
	vocab *release.Vocabulary
}

// NewVocab loads the vocabulary from the store.
//
// A load failure is not fatal: matching still works, the parser just reads
// fewer titles. Failing open is deliberate — a database hiccup must not stop
// the pipeline, and an empty vocabulary is the same state a fresh install
// starts in.
func NewVocab(st *store.Store) *Vocab {
	v := &Vocab{vocab: release.NewVocabulary()}
	v.Refresh(st)
	return v
}

// Refresh re-reads the vocabulary from the store. Call after learning a new
// token, so the next parse honours it.
//
// A nil store is a no-op rather than a panic: a caller with no database — a
// test exercising pure decision logic, say — still gets a usable, empty
// vocabulary.
func (v *Vocab) Refresh(st *store.Store) {
	if st == nil {
		return
	}
	entries, err := st.Vocabulary()
	if err != nil {
		// Keep the current vocabulary rather than blanking it: a failed
		// refresh must not unlearn everything the user has taught.
		return
	}
	next := release.NewVocabulary()
	next.Load(entries)
	v.mu.Lock()
	v.vocab = next
	v.mu.Unlock()
}

// Apply applies the vocabulary to a parsed release.
func (v *Vocab) Apply(r *release.Release) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	v.vocab.ApplyVocabulary(r)
}

// Parse parses a title with the vocabulary applied.
//
// The same path every enforcement point uses, so a caller that only has the
// vocabulary — no show — reads titles the way the pipeline will.
func (v *Vocab) Parse(title string) release.Release {
	r := release.Parse(title)
	v.Apply(&r)
	return r
}

// Learn records a token's canonical value in the store and in the cache.
//
// Both, in that order: the store is the durable record, and the cache is what
// the next parse reads. Writing only the store would leave the running
// pipeline reading a stale vocabulary until the next refresh, so a token the
// user just taught would appear not to have been learned.
func (v *Vocab) Learn(st *store.Store, kind, token, canonical string) error {
	if err := st.LearnVocabulary(kind, token, canonical); err != nil {
		return err
	}
	v.Refresh(st)
	return nil
}

// Show builds a matcher view of a stored show, using this vocabulary.
//
// The offsets are read per show because they are per show; the vocabulary is
// shared because it is not.
func (v *Vocab) Show(st *store.Store, sh *store.Show) (*Show, error) {
	off, err := st.GroupOffsets(sh.ID)
	if err != nil {
		return nil, err
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return NewShow(sh, off, v.vocab), nil
}
