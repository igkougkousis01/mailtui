package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/igkougkousis01/mailtui/internal/store"
)

// Run draws the inbox for st and blocks until the user quits or ctx is
// cancelled. smtpAddr is shown in the header and is only ever displayed; this
// package knows nothing about the server behind it.
//
// The subscription is owned here rather than by the Model, so that it is
// cancelled by the same defer that ends the program however it ends. That
// closes the channel, which unblocks the goroutine waiting on it, so nothing
// is left running when Run returns.
func Run(ctx context.Context, st *store.Store, smtpAddr string) error {
	events, unsubscribe := st.Subscribe()
	defer unsubscribe()

	p := tea.NewProgram(New(st, smtpAddr, events), tea.WithContext(ctx))

	_, err := p.Run()
	return err
}
