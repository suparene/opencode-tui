// chinook-tui is a small terminal UI that browses artists and albums from the
// Chinook database hosted on Supabase.
//
//   - Shows a paginated, filterable list of artists with their album count.
//   - Pressing enter on an artist shows that artist's album titles (paginated).
//   - Esc goes back to the artist list, q quits.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const pageSize = 10

var (
	supabaseURL = envOr("SUPABASE_URL", "https://dehkuehlbszvpjxingoy.supabase.co")
	supabaseKey = envOr("SUPABASE_KEY", "sb_publishable_P0UfD0k1TsdZ9gUIKp1Fsg_YOdF6pYv")
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ---------------------------------------------------------------------------
// API types & client (Supabase PostgREST)
// ---------------------------------------------------------------------------

type artistRow struct {
	ID   int    `json:"artist_id"`
	Name string `json:"name"`
	// PostgREST embedded count: select=...,album(count)
	Albums []struct {
		Count int `json:"count"`
	} `json:"album"`
}

type Artist struct {
	ID         int
	Name       string
	AlbumCount int
}

func (r artistRow) toArtist() Artist {
	n := 0
	if len(r.Albums) > 0 {
		n = r.Albums[0].Count
	}
	return Artist{ID: r.ID, Name: r.Name, AlbumCount: n}
}

type albumRow struct {
	Title string `json:"title"`
}

func getJSON(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, supabaseURL+"/rest/v1/"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("apikey", supabaseKey)
	req.Header.Set("Authorization", "Bearer "+supabaseKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("supabase: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

// fetchArtists returns all artists with their album counts in one request.
func fetchArtists() ([]Artist, error) {
	var rows []artistRow
	if err := getJSON("artist?select=artist_id,name,album(count)&order=name.asc", &rows); err != nil {
		return nil, err
	}
	artists := make([]Artist, 0, len(rows))
	for _, r := range rows {
		artists = append(artists, r.toArtist())
	}
	return artists, nil
}

// fetchAlbums returns the album titles for one artist.
func fetchAlbums(artistID int) ([]string, error) {
	var rows []albumRow
	path := fmt.Sprintf("album?select=title&artist_id=eq.%d&order=album_id.asc", artistID)
	if err := getJSON(path, &rows); err != nil {
		return nil, err
	}
	titles := make([]string, 0, len(rows))
	for _, r := range rows {
		titles = append(titles, r.Title)
	}
	return titles, nil
}

// ---------------------------------------------------------------------------
// List items
// ---------------------------------------------------------------------------

type artistItem struct{ a Artist }

func (i artistItem) Title() string { return i.a.Name }
func (i artistItem) Description() string { // shown to the right of the title
	if i.a.AlbumCount == 1 {
		return "1 album"
	}
	return fmt.Sprintf("%d albums", i.a.AlbumCount)
}

func (i artistItem) FilterValue() string { return i.a.Name }

type albumItem struct{ title string }

func (i albumItem) Title() string       { return i.title }
func (i albumItem) Description() string { return "" }

func (i albumItem) FilterValue() string { return i.title }

// ---------------------------------------------------------------------------
// Messages & commands
// ---------------------------------------------------------------------------

type artistsLoadedMsg struct {
	artists []Artist
	err     error
}

func loadArtists() tea.Cmd {
	return func() tea.Msg {
		a, err := fetchArtists()
		return artistsLoadedMsg{artists: a, err: err}
	}
}

type albumsLoadedMsg struct {
	artistID int
	titles   []string
	err      error
}

func loadAlbums(artistID int) tea.Cmd {
	return func() tea.Msg {
		t, err := fetchAlbums(artistID)
		return albumsLoadedMsg{artistID: artistID, titles: t, err: err}
	}
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

type screen int

const (
	screenArtists screen = iota
	screenAlbums
)

type model struct {
	artists    []Artist
	artistList list.Model
	albumList  list.Model

	selectedArtist *Artist
	screen         screen
	loading        bool
	err            error

	width  int // list width in cells
	height int // list height in lines (excludes the hint line)
}

func newModel() model {
	m := model{screen: screenArtists, loading: true, width: 80, height: 24}
	m.artistList = m.newList(nil, "Chinook — Artists")
	m.albumList = m.newList(nil, "Albums")
	return m
}

// newList builds a list model with the app's keybindings and size.
func (m *model) newList(items []list.Item, title string) list.Model {
	l := list.New(items, list.NewDefaultDelegate(), m.width, m.height)
	l.Title = title
	// The default quit binding is "q" and "esc"; we want esc for going back.
	l.KeyMap.Quit = key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit"))
	return l
}

func (m *model) buildArtistList() {
	items := make([]list.Item, 0, len(m.artists))
	for _, a := range m.artists {
		items = append(items, artistItem{a: a})
	}
	m.artistList = m.newList(items, "Chinook — Artists")
}

func (m *model) buildAlbumList(titles []string) {
	items := make([]list.Item, 0, len(titles))
	for _, t := range titles {
		items = append(items, albumItem{title: t})
	}
	name := ""
	if m.selectedArtist != nil {
		name = "Albums by " + m.selectedArtist.Name
	} else {
		name = "Albums"
	}
	m.albumList = m.newList(items, name)
}

// activeList returns the list model for the current screen.
func (m *model) activeList() *list.Model {
	if m.screen == screenAlbums {
		return &m.albumList
	}
	return &m.artistList
}

// updateActiveList forwards a message to the list of the current screen.
func (m *model) updateActiveList(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.screen {
	case screenArtists:
		m.artistList, cmd = m.artistList.Update(msg)
	default:
		m.albumList, cmd = m.albumList.Update(msg)
	}
	return m, cmd
}

func (m model) Init() tea.Cmd { return loadArtists() }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case artistsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.artists = msg.artists
		m.buildArtistList()
		return m, nil

	case albumsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.buildAlbumList(msg.titles)
		m.screen = screenAlbums
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		// Leave one line for the blank separator and one for the hint.
		m.height = max(msg.Height-2, 1)
		m.artistList.SetSize(m.width, m.height)
		m.albumList.SetSize(m.width, m.height)
		return m, nil

	case tea.KeyMsg:
		active := m.activeList()
		if active.FilterState() == list.Filtering {
			// While typing a filter, let the list handle every key.
			return m.updateActiveList(msg)
		}

		switch msg.String() {
		case "enter":
			if m.screen == screenArtists {
				item, ok := active.SelectedItem().(artistItem)
				if !ok {
					return m, nil
				}
				m.selectedArtist = &item.a
				m.loading = true
				return m, loadAlbums(item.a.ID)
			}

		case "esc":
			if active.FilterState() == list.FilterApplied {
				return m.updateActiveList(msg) // clear the filter first
			}
			if m.screen == screenAlbums {
				m.screen = screenArtists // back to the artist list
			} else {
				return m, tea.Quit
			}
			return m, nil

		case "ctrl+c":
			return m, tea.Quit
		}
		// Everything else (arrows, pgup/pgdn, /, q, ...) goes to the list.
		return m.updateActiveList(msg)
	}

	return m.updateActiveList(msg)
}

// ---------------------------------------------------------------------------
// View & styles
// ---------------------------------------------------------------------------

var (
	hintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

func (m model) View() string {
	if m.loading {
		return "Loading..."
	}
	if m.err != nil {
		return errStyle.Render("Error: "+m.err.Error()) + "\n\n" + hintStyle.Render("q to quit")
	}

	var b strings.Builder
	switch m.screen {
	case screenArtists:
		b.WriteString(m.artistList.View())
		b.WriteString("\n" + hintStyle.Render("enter: view albums · q: quit") + "\n")
	case screenAlbums:
		if len(m.albumList.Items()) == 0 {
			b.WriteString(hintStyle.Render("No albums for this artist.") + "\n")
		} else {
			b.WriteString(m.albumList.View())
		}
		b.WriteString("\n" + hintStyle.Render("esc: back · q: quit") + "\n")
	}
	return b.String()
}

func main() {
	p := tea.NewProgram(newModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
