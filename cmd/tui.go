package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Live dashboard: every worktree's env, database and git state",
	RunE:  runTUI,
}

func init() { rootCmd.AddCommand(tuiCmd) }

// runTUI is the same adapter every other command is — it gathers nothing and
// decides nothing. The board does its own I/O because the whole point is that
// the rows arrive one at a time rather than all at the end.
func runTUI(cmd *cobra.Command, args []string) error {
	cwd, err := worktreeRoot()
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(newBoard(cwd), tea.WithAltScreen()).Run()
	return err
}

// fleetMsg is the one step the board has to wait on: git's worktree list, the
// main checkout, and the single psql round trip. Everything after it streams.
type fleetMsg struct {
	refs   []check.Ref
	source check.Worktree
	d      check.Doctor
	err    error
}

// rowMsg is one worktree's row landing. Rows are addressed by index into refs
// rather than by path, so a row can never overwrite the wrong one.
type rowMsg struct {
	i int
	s check.Status
}

// detailMsg carries doctor's own report, already rendered — the drill-in shows
// what `th doctor` shows because printReport wrote both.
type detailMsg struct {
	i      int
	report string
	err    error
}

// hydratedMsg is `th hydrate` exiting. The error is the subprocess's, not ours.
type hydratedMsg struct {
	i   int
	err error
}

type board struct {
	cwd    string
	fleet  fleetMsg
	rows   []check.Status
	done   []bool // a row with no spinner: check.Row has answered for it
	cursor int

	spin    spinner.Model
	detail  viewport.Model
	showing bool // in the drill-in rather than the grid
	loading bool // a detail run is in flight

	ready bool // a WindowSizeMsg has arrived, so the viewport has real dimensions
	w, h  int
	note  string // the last thing that happened, one line
}

func newBoard(cwd string) board {
	s := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(emptyStyle))
	return board{cwd: cwd, spin: s}
}

func (b board) Init() tea.Cmd { return tea.Batch(b.spin.Tick, fleetCmd(b.cwd)) }

// fleetCmd does the blocking setup off the render loop. It calls the same
// gatherer `th ls` does, so the two views cannot disagree about which databases
// exist or which branch is main.
func fleetCmd(cwd string) tea.Cmd {
	return func() tea.Msg {
		refs, source, d, err := fleet(cwd)
		return fleetMsg{refs: refs, source: source, d: d, err: err}
	}
}

// rowCmd is one worktree's gather. Returning one of these per worktree is what
// makes cells fill in progressively: bubbletea runs every batched Cmd in its own
// goroutine and delivers each result the moment it lands, so the grid never
// waits on the slowest worktree. That is the whole reason check.Row exists as a
// per-worktree call — no WaitGroup here, the runtime already is one.
func rowCmd(f fleetMsg, i int, cwd string) tea.Cmd {
	return func() tea.Msg {
		s := f.d.Row(f.refs[i], f.source)
		s.Current = samePath(f.refs[i].Path, cwd)
		return rowMsg{i, s}
	}
}

// detailCmd renders doctor's report for one worktree into a buffer. It calls
// diagnose — the same function `th doctor` and `th new` call — so the drill-in
// is a second face on one answer, never a second answer.
func detailCmd(i int, path string) tea.Cmd {
	return func() tea.Msg {
		findings, checks, err := diagnose(path)
		if err != nil {
			return detailMsg{i: i, err: err}
		}
		var out strings.Builder
		printReport(&out, findings, checks, path)
		return detailMsg{i: i, report: out.String()}
	}
}

// hydrateCmd runs `th hydrate` as a SUBPROCESS, and that is a correctness
// constraint rather than a preference. In-process it would write straight to
// os.Stdout — say/sayln and deps.RunRecreate both do — and shred the frame; it
// would also set the package-level flag globals, which are only safe because
// exactly one command runs per process. tea.ExecProcess hands the terminal over
// and takes it back, so hydrate's own output scrolls normally and the grid
// returns intact.
func (b board) hydrateCmd() tea.Cmd {
	self, err := os.Executable()
	if err != nil {
		return func() tea.Msg { return hydratedMsg{b.cursor, err} }
	}
	i := b.cursor
	c := exec.Command(self, "hydrate")
	c.Dir = b.fleet.refs[i].Path
	return tea.ExecProcess(c, func(err error) tea.Msg { return hydratedMsg{i, err} })
}

func (b board) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		b.w, b.h = msg.Width, msg.Height
		b.detail.Width, b.detail.Height = msg.Width, max(msg.Height-3, 1)
		b.ready = true
		return b, nil

	case tea.KeyMsg:
		return b.key(msg)

	case fleetMsg:
		b.fleet = msg
		if msg.err != nil {
			b.note = msg.err.Error()
			return b, nil
		}
		// One slot per worktree, preallocated: every row lands in the slot it was
		// issued for, so results may arrive in any order without racing.
		b.rows = make([]check.Status, len(msg.refs))
		b.done = make([]bool, len(msg.refs))
		b.cursor = min(b.cursor, len(msg.refs)-1)
		cmds := make([]tea.Cmd, len(msg.refs))
		for i := range msg.refs {
			cmds[i] = rowCmd(msg, i, b.cwd)
		}
		return b, tea.Batch(cmds...)

	case rowMsg:
		if msg.i < len(b.rows) {
			b.rows[msg.i], b.done[msg.i] = msg.s, true
		}
		return b, nil

	case detailMsg:
		b.loading = false
		if msg.i != b.cursor {
			return b, nil // the cursor moved on: this answer is about another row
		}
		body := msg.report
		if msg.err != nil {
			body = "could not read this worktree: " + msg.err.Error()
		}
		b.detail.SetContent(body)
		b.detail.GotoTop()
		b.showing = true
		return b, nil

	case hydratedMsg:
		b.note = "hydrated " + filepath.Base(b.fleet.refs[msg.i].Path)
		if msg.err != nil {
			b.note = "hydrate failed: " + oneLine(msg.err)
		}
		// Re-ask about the row that changed and nothing else — the cells flip in
		// place, which is the point of doing this from the dashboard at all.
		return b, b.refresh(msg.i)

	case spinner.TickMsg:
		var cmd tea.Cmd
		b.spin, cmd = b.spin.Update(msg)
		return b, cmd
	}
	return b, nil
}

// refresh re-runs one row, and the drill-in with it when that row is the one
// on screen — a hydrate that fixed the env should not leave a stale report up.
func (b *board) refresh(i int) tea.Cmd {
	if i >= len(b.done) {
		return nil
	}
	b.done[i] = false
	cmds := []tea.Cmd{rowCmd(b.fleet, i, b.cwd)}
	if b.showing && i == b.cursor {
		b.loading = true
		cmds = append(cmds, detailCmd(i, b.fleet.refs[i].Path))
	}
	return tea.Batch(cmds...)
}

func (b board) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return b, tea.Quit
	case "esc":
		if b.showing {
			b.showing = false
			return b, nil
		}
		return b, tea.Quit
	}

	// The drill-in is a scroller: give it every other key so page-up, home and
	// the arrows behave the way a pager is expected to.
	if b.showing {
		switch msg.String() {
		case "h":
			return b, b.hydrateCmd()
		case "r":
			return b, b.refresh(b.cursor)
		}
		var cmd tea.Cmd
		b.detail, cmd = b.detail.Update(msg)
		return b, cmd
	}

	switch msg.String() {
	case "up", "k":
		b.cursor = max(b.cursor-1, 0)
	case "down", "j":
		b.cursor = min(b.cursor+1, len(b.fleet.refs)-1)
	case "enter":
		// One detail run at a time: diagnose sets a package-level psql prefix, and
		// two of them in flight would be two goroutines writing it at once.
		if len(b.fleet.refs) > 0 && !b.loading {
			b.loading = true
			return b, detailCmd(b.cursor, b.fleet.refs[b.cursor].Path)
		}
	case "h":
		if len(b.fleet.refs) > 0 {
			return b, b.hydrateCmd()
		}
	case "r":
		b.note = ""
		return b, fleetCmd(b.cwd)
	}
	return b, nil
}

func (b board) View() string {
	if b.fleet.err != nil {
		return "\n  " + missingStyle.Render(b.fleet.err.Error()) + "\n\n  press q to quit\n"
	}
	if len(b.fleet.refs) == 0 {
		return "\n  " + b.spin.View() + " reading the fleet…\n"
	}
	if b.showing {
		return b.detailView()
	}

	var out strings.Builder
	out.WriteString(b.grid())
	out.WriteString("\n")
	if b.note != "" {
		out.WriteString("  " + b.note + "\n")
	}
	out.WriteString(helpStyle.Render(
		"  ↑/↓ select · enter detail · h hydrate · r refresh · q quit"))
	return out.String()
}

// grid is printFleet's table with two additions and no new judgment: the cursor
// row is highlighted, and a row that check.Row has not answered for yet shows a
// spinner in every cell it has not filled. Path and branch come straight from
// git's porcelain, so they are on screen before any check runs.
func (b board) grid() string {
	t := table.New().
		Border(lipgloss.NormalBorder()).
		StyleFunc(func(row, col int) lipgloss.Style {
			switch row {
			case table.HeaderRow:
				return headerStyle.Padding(0, 1)
			case b.cursor:
				return selectedStyle.Padding(0, 1)
			}
			return lipgloss.NewStyle().Padding(0, 1)
		}).
		Headers("WORKTREE", "BRANCH", "ENV", "DB", "BEHIND", "DIRTY")

	for i, ref := range b.fleet.refs {
		name := filepath.Base(ref.Path)
		if samePath(ref.Path, b.cwd) {
			name = "* " + name // where the human is standing
		}
		if !b.done[i] {
			// A blank branch is git's answer for detached or bare, and turning that
			// into a word is check.Status's job — spin until it has said so.
			branch := ref.Branch
			if branch == "" {
				branch = b.spin.View()
			}
			sp := b.spin.View()
			t.Row(name, branch, sp, sp, sp, sp)
			continue
		}
		r := b.rows[i]
		t.Row(name, r.Branch, envCell(r.Env), dbCell(r.DB), behindCell(r.Behind), dirtyCell(r.Dirty))
	}
	return t.String()
}

func (b board) detailView() string {
	head := headerStyle.Render("  " + filepath.Base(b.fleet.refs[b.cursor].Path))
	body := b.detail.View()
	if b.loading {
		body = "  " + b.spin.View() + " checking…"
	}
	if !b.ready {
		// No WindowSizeMsg yet, so the viewport has zero height and would render
		// nothing at all. Show the report unclipped rather than a blank screen.
		body = b.detail.View()
	}
	return head + "\n" + body + "\n" +
		helpStyle.Render("  ↑/↓ scroll · h hydrate · r refresh · esc back · q quit")
}

var (
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")) // blue
	helpStyle     = lipgloss.NewStyle().Faint(true)
)
