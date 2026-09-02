package library

import (
	"fmt"
	"sync"
	"testing"
)

// TestStore_ConcurrentAccess hammers every exported Store method from many
// goroutines at once, against a store that is also persisting to disk. Run
// under -race (the project's standard `go test -race`) this proves the
// mutex discipline copied from patch.Store actually holds: no unsynchronized
// map/slice access, and no value handed out by Get/List/Find aliasing the
// live library (which is why cloneRecord copies every nested map and slice
// — a shallow copy would show up here as a data race on a mode's channel
// map, not as a wrong answer).
func TestStore_ConcurrentAccess(t *testing.T) {
	st := NewStore(t.TempDir() + "/lib.json")

	const workers = 8
	const iterations = 40
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				rec := sampleRecord()
				rec.Manufacturer = fmt.Sprintf("Maker%d", w)
				rec.Model = fmt.Sprintf("Model%d", i%5)
				st.Upsert(rec)

				// Readers, including ones that walk the nested maps of a
				// value they were handed while another goroutine merges
				// into the live record it was copied from.
				for _, r := range st.List() {
					for _, m := range r.Modes {
						for off := range m.ChannelFunctions {
							_ = m.ChannelFunctions[off].Attribute
						}
					}
				}
				st.Find("", fmt.Sprintf("Model%d", i%5))
				st.GetByKey(KeyFor(rec.Manufacturer, rec.Model))
				lib := st.Get()
				for j := range lib.Records {
					lib.Records[j].SupportedPIDs = append(lib.Records[j].SupportedPIDs, 0xFFFF)
				}
				st.Export("test", lib.ModifiedAt)

				if i%7 == 0 {
					st.Import(docWith(Record{
						Manufacturer: fmt.Sprintf("Import%d", w),
						Model:        fmt.Sprintf("M%d", i%3),
					}), ModeMerge)
				}
				if i%11 == 0 {
					st.Delete(KeyFor(fmt.Sprintf("Maker%d", w), "Model0"))
				}
			}
		}(w)
	}
	wg.Wait()

	// The mutation the readers performed on their handed-out copies must
	// not have reached the live store.
	for _, r := range st.List() {
		for _, p := range r.SupportedPIDs {
			if p == 0xFFFF {
				t.Fatalf("record %q was mutated through a value handed out by Get — the copy is not deep enough", r.Key)
			}
		}
	}
}
