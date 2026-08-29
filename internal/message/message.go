// Package message turns a raw SMTP DATA payload into a structured Message.
//
// It owns all RFC 5322 / MIME knowledge in mailtui; the smtp package knows
// only about the envelope and hands the raw bytes here.
package message

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	gomessage "github.com/emersion/go-message"
	// Registering the charset reader lets the parser decode non-UTF-8 bodies
	// and encoded-words such as ISO-8859-1 and Windows-1252.
	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
)

// Envelope is the SMTP-level addressing of a message: what the sending client
// said in MAIL FROM and RCPT TO. It is deliberately kept apart from the From
// and To headers inside the message, which a sender is free to set to anything.
type Envelope struct {
	From string
	To   []string
}

// Message is one captured email: its SMTP envelope, the parsed headers and
// bodies, and the exact bytes we received.
type Message struct {
	// EnvelopeFrom and EnvelopeTo come from MAIL FROM and RCPT TO. They decide
	// where mail actually went.
	EnvelopeFrom string
	EnvelopeTo   []string

	// HeaderFrom and HeaderTo come from the From and To headers. They are what
	// a mail client displays, and need not match the envelope.
	HeaderFrom string
	HeaderTo   []string

	Subject  string
	TextBody string
	HTMLBody string

	// Raw is the DATA payload exactly as captured.
	Raw []byte

	ReceivedAt time.Time

	// ParseError records why the payload could not be read as a mail message.
	// It is nil for a message that parsed. When it is set, the envelope, Raw
	// and ReceivedAt are still accurate and the fields above are empty or
	// incomplete: a message we cannot understand is often the one a developer
	// most wants to look at, so it is kept rather than discarded.
	//
	// It lives on the Message, not in a return value, because it describes the
	// captured artifact and has to travel with it into storage and the TUI.
	ParseError error
}

// Capture records the raw DATA payload received for env as a Message.
//
// It never fails. Whatever the payload turns out to be, the envelope, Raw and
// ReceivedAt are filled in; if the content cannot be parsed, the reason is left
// in Message.ParseError. A message that merely uses a charset we do not know is
// parsed on a best-effort basis and is not treated as a failure.
func Capture(env Envelope, raw []byte, receivedAt time.Time) *Message {
	msg := &Message{
		EnvelopeFrom: env.From,
		EnvelopeTo:   append([]string(nil), env.To...),
		Raw:          append([]byte(nil), raw...),
		ReceivedAt:   receivedAt,
	}

	msg.ParseError = msg.parse()

	return msg
}

// parse fills in the header and body fields from m.Raw. Whatever it manages to
// read before an error is kept, so a message that goes wrong halfway is still
// worth more than an empty one.
func (m *Message) parse() error {
	mr, err := mail.CreateReader(bytes.NewReader(m.Raw))
	if err != nil {
		// An unknown charset still yields a usable reader, so keep going and
		// decode whatever we can.
		if !gomessage.IsUnknownCharset(err) {
			return fmt.Errorf("parse mail: %w", err)
		}
	}
	defer mr.Close()

	m.Subject = subject(&mr.Header)
	m.HeaderFrom = strings.Join(addressList(&mr.Header, "From"), ", ")
	m.HeaderTo = addressList(&mr.Header, "To")

	if err := m.readBodies(mr); err != nil {
		return fmt.Errorf("parse mail body: %w", err)
	}

	return nil
}

// readBodies walks the MIME tree and collects the text and HTML parts.
// Attachments are skipped: this milestone has nowhere to put them.
func (m *Message) readBodies(mr *mail.Reader) error {
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil && !gomessage.IsUnknownCharset(err) {
			return err
		}

		inline, ok := part.Header.(*mail.InlineHeader)
		if !ok {
			continue
		}

		body, err := io.ReadAll(part.Body)
		if err != nil {
			return err
		}

		contentType, _, err := inline.ContentType()
		if err != nil {
			// A part with an unparsable Content-Type is treated as plain text,
			// which is also what a missing Content-Type means.
			contentType = "text/plain"
		}

		switch contentType {
		case "text/html":
			m.HTMLBody = appendPart(m.HTMLBody, string(body))
		default:
			m.TextBody = appendPart(m.TextBody, string(body))
		}
	}
}

// appendPart joins sibling parts of the same type rather than letting a later
// one silently win, so a multipart/mixed body is not truncated to its first part.
func appendPart(existing, addition string) string {
	if existing == "" {
		return addition
	}
	return existing + "\n" + addition
}

// subject returns the decoded Subject, falling back to the undecoded value so a
// broken encoded-word costs us the decoding rather than the whole header.
func subject(h *mail.Header) string {
	s, err := h.Subject()
	if err != nil {
		return h.Get("Subject")
	}
	return s
}

// addressList returns the addresses in an address header. A header we cannot
// parse as an address list is kept as its raw text: for a catcher, showing what
// the sender actually wrote beats showing nothing.
func addressList(h *mail.Header, key string) []string {
	addrs, err := h.AddressList(key)
	if err != nil {
		if raw := h.Get(key); raw != "" {
			return []string{raw}
		}
		return nil
	}

	list := make([]string, 0, len(addrs))
	for _, a := range addrs {
		list = append(list, formatAddress(a))
	}
	if len(list) == 0 {
		return nil
	}
	return list
}

// formatAddress renders an address for a human to read. Address.String would
// re-encode a non-ASCII display name back into an RFC 2047 encoded-word, which
// undoes the decoding we just did.
func formatAddress(a *mail.Address) string {
	if a.Name == "" {
		return a.Address
	}
	return fmt.Sprintf("%s <%s>", a.Name, a.Address)
}
