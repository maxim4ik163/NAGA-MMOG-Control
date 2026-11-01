package main

import (
	"fmt"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Left-button chatter filter
//
// A worn switch turns one press into "down, up, down" a few ms apart. The
// filter holds back every release for a short while; if a press follows
// within that window, the release and the press are both dropped, so the
// application sees one long press. Two rules (measured on the user's mouse,
// docs/PROTOCOL.md):
//   - a release shorter than T is a bounce;
//   - a release shorter than G is a bounce if the press before it lasted
//     less than H (real presses last 50+ ms, bounce fragments a few ms).
// Holding back the release (instead of dropping the second press) keeps
// drags intact: a bounce in the middle of a drag never drops the object.

type chatterParams struct {
	T, H, G, D time.Duration
}

func (c Chatter) params() chatterParams {
	return chatterParams{
		T: time.Duration(c.T) * time.Millisecond,
		H: time.Duration(c.H) * time.Millisecond,
		G: time.Duration(c.G) * time.Millisecond,
		D: time.Duration(c.D) * time.Millisecond,
	}
}

// wait returns how long a release after a press of length hold is held back.
func (p chatterParams) wait(hold time.Duration) time.Duration {
	w := p.T
	if hold < p.H && p.G > w {
		w = p.G
	}
	return w
}

// chatterFilter is driven by the mouse hook. now() and after() are injected
// so the logic can be tested without real time.
type chatterFilter struct {
	mu        sync.Mutex
	p         chatterParams
	on        bool
	lastDown  time.Duration // time of the last physical press (merged or not)
	pending   bool          // a release is being held back
	dragging  bool          // the cursor moved while the button was held
	gen       int           // invalidates scheduled releases
	emitUp    func()        // sends the held-back release (SendInput)
	after     func(d time.Duration, f func())
	merged    int // statistics: bounces removed
	seenDowns int
}

func newChatterFilter(emitUp func(), after func(time.Duration, func())) *chatterFilter {
	return &chatterFilter{emitUp: emitUp, after: after}
}

func (f *chatterFilter) set(on bool, p chatterParams) {
	f.mu.Lock()
	f.on, f.p = on, p
	f.mu.Unlock()
	if !on {
		f.flush()
	}
}

// down handles a physical press at time now; false = swallow it.
func (f *chatterFilter) down(now time.Duration) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seenDowns++
	f.lastDown = now
	if !f.pending {
		f.dragging = false // a new press starts; a merged bounce keeps the drag
	}
	if f.pending {
		// bounce: the application still sees the button down
		f.pending = false
		f.gen++
		f.merged++
		return false
	}
	return true
}

// up handles a physical release; false = swallow it (it will be sent later
// unless a press follows in time).
func (f *chatterFilter) up(now time.Duration) bool {
	f.mu.Lock()
	if !f.on {
		f.mu.Unlock()
		return true
	}
	w := f.p.wait(now - f.lastDown)
	if f.dragging && f.p.D > w {
		w = f.p.D
	}
	if w <= 0 {
		f.mu.Unlock()
		return true
	}
	f.pending = true
	f.gen++
	g := f.gen
	f.mu.Unlock()
	f.after(w, func() {
		f.mu.Lock()
		send := f.pending && f.gen == g
		if send {
			f.pending = false
		}
		f.mu.Unlock()
		if send {
			f.emitUp()
		}
	})
	return false
}

// setDragging marks the current press as a drag (called by the hook when
// the cursor has moved far enough with the button held).
func (f *chatterFilter) setDragging() {
	f.mu.Lock()
	f.dragging = true
	f.mu.Unlock()
}

// flush sends a held-back release now (filter switched off, app exiting).
func (f *chatterFilter) flush() {
	f.mu.Lock()
	send := f.pending
	f.pending = false
	f.gen++
	f.mu.Unlock()
	if send {
		f.emitUp()
	}
}

func (f *chatterFilter) stats() (merged, downs int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.merged, f.seenDowns
}

// ---------------------------------------------------------------------------
// Calibration
//
// The user clicks slowly (single clicks) and then double-clicks; the hook
// records every physical press and release. For each parameter set we replay
// the recording through the filter rules and count how many intended clicks
// come out right: one press per single click, two per double click.

type clickEvent struct {
	T    time.Duration `json:"t"` // since the start of the recording
	Down bool          `json:"down"`
}

// groupGap separates intended clicks: users pause between attempts.
const groupGap = 300 * time.Millisecond

// simulate returns the times of the presses that pass the filter.
func simulate(ev []clickEvent, p chatterParams) []time.Duration {
	var out []time.Duration
	var lastDown, lastUp time.Duration = -1, -1
	var lastHold time.Duration = -1
	for _, e := range ev {
		if e.Down {
			merge := false
			if lastUp >= 0 {
				gap := e.T - lastUp
				merge = gap < p.T || (lastHold >= 0 && lastHold < p.H && gap < p.G)
			}
			if !merge {
				out = append(out, e.T)
			}
			lastDown = e.T
		} else if lastDown >= 0 {
			lastHold = e.T - lastDown
			lastUp = e.T
		}
	}
	return out
}

// groups clusters presses into intended clicks and returns their sizes.
func groups(presses []time.Duration) []int {
	var g []int
	n := 0
	var last time.Duration = -1
	for _, t := range presses {
		if last >= 0 && t-last > groupGap {
			g = append(g, n)
			n = 0
		}
		n++
		last = t
	}
	if n > 0 {
		g = append(g, n)
	}
	return g
}

func countSize(g []int, size int) (ok, total int) {
	for _, n := range g {
		if n == size {
			ok++
		}
	}
	return ok, len(g)
}

type calibResult struct {
	T, H, G      int
	SinglesOK    int     `json:"singlesOk"`
	Singles      int     `json:"singles"`
	DoublesOK    int     `json:"doublesOk"`
	Doubles      int     `json:"doubles"`
	RawSinglesOK int     `json:"rawSinglesOk"`
	RawDoublesOK int     `json:"rawDoublesOk"`
	ChatterSeen  int     `json:"chatterSeen"` // bounces in the single-click run
	MaxChatterMs float64 `json:"maxChatterMs"`
	Score        string  `json:"score"`
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// fitChatter searches the parameter grid. Ties go to the shortest delay.
func fitChatter(single, double []clickEvent) calibResult {
	var best calibResult
	bestScore, bestDelay := -1, 1<<30
	for T := 0; T <= 100; T += 5 {
		for _, H := range []int{0, 10, 15, 20, 25, 30} {
			for _, G := range []int{0, 60, 80, 100, 120, 150} {
				if H == 0 && G != 0 || H != 0 && (G == 0 || G < T) {
					continue // rule B needs both H and G; without it G is unused
				}
				p := chatterParams{T: time.Duration(T) * time.Millisecond, H: time.Duration(H) * time.Millisecond, G: time.Duration(G) * time.Millisecond}
				s1, n1 := countSize(groups(simulate(single, p)), 1)
				s2, n2 := countSize(groups(simulate(double, p)), 2)
				score := s1 + s2
				delay := T
				if G > delay {
					delay = G
				}
				if score > bestScore || score == bestScore && (delay < bestDelay || delay == bestDelay && T < best.T) {
					bestScore, bestDelay = score, delay
					best = calibResult{T: T, H: H, G: G, SinglesOK: s1, Singles: n1, DoublesOK: s2, Doubles: n2}
				}
			}
		}
	}
	best.RawSinglesOK, _ = countSize(groups(simulate(single, chatterParams{})), 1)
	best.RawDoublesOK, _ = countSize(groups(simulate(double, chatterParams{})), 2)
	// chatter statistics of the single-click run: every release inside a group
	var lastUp time.Duration = -1
	for _, e := range single {
		if e.Down && lastUp >= 0 {
			if gap := e.T - lastUp; gap < groupGap {
				best.ChatterSeen++
				if ms(gap) > best.MaxChatterMs {
					best.MaxChatterMs = ms(gap)
				}
			}
		}
		if !e.Down {
			lastUp = e.T
		}
	}
	best.Score = fmt.Sprintf(T("одиночные %d/%d, двойные %d/%d (без фильтра %d/%d и %d/%d)",
		"singles %d/%d, doubles %d/%d (no filter %d/%d and %d/%d)"),
		best.SinglesOK, best.Singles, best.DoublesOK, best.Doubles,
		best.RawSinglesOK, best.Singles, best.RawDoublesOK, best.Doubles)
	return best
}

// trimRecording removes the clicks that belong to the wizard's own buttons:
// the tail of the "Start" click (bounces right after recording begins) and
// the "Done" click (the last group, if it ended shortly before stopping).
func trimRecording(ev []clickEvent, stopAt time.Duration) []clickEvent {
	const lead = 250 * time.Millisecond
	const doneWindow = 1500 * time.Millisecond
	i := 0
	for i < len(ev) && ev[i].T < lead {
		i++
	}
	// an "up" without its "down" at the start is meaningless
	for i < len(ev) && !ev[i].Down {
		i++
	}
	ev = ev[i:]
	if len(ev) == 0 {
		return ev
	}
	// find the start of the last group of presses
	last := len(ev) - 1
	start := 0
	var lastUp time.Duration = -1
	for j, e := range ev {
		if e.Down && lastUp >= 0 && e.T-lastUp > groupGap {
			start = j
		}
		if !e.Down {
			lastUp = e.T
		}
	}
	if stopAt-ev[last].T < doneWindow && start > 0 {
		ev = ev[:start]
	}
	return ev
}
