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
	"slices"
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

// Header is one field of the message header block: the name as the sender
// spelled it, and the value with any folding removed.
//
// Headers are kept as an ordered slice rather than a map because the header
// block is a sequence, not a dictionary: order carries meaning (Received lines
// read as a delivery trace) and a key may legitimately appear more than once
// (Received again, Comments, most X- headers). A map would throw both away.
//
// The value is unfolded, which is a small, reversible normalisation: the exact
// bytes, folding and all, are still in Message.Raw.
type Header struct {
	Key   string
	Value string
}

// Attachment is what we know about one non-body MIME part.
//
// It is metadata only. The payload is counted and discarded as the message is
// parsed, so a 20 MB PDF costs us this struct rather than a second copy of the
// file; Message.Raw still holds the encoded original if anyone needs the bytes.
type Attachment struct {
	// Filename is the name the sender gave the part, decoded from any RFC 2047
	// or RFC 2231 encoding. It is empty for a part that was sent without one,
	// which is normal for an inline image identified only by its Content-ID.
	Filename string

	// ContentType is the media type without its parameters, defaulting to
	// text/plain as RFC 2045 requires when the part declares none.
	ContentType string

	// Size is the length of the decoded payload in bytes: what the file would
	// be if it were saved, not how many bytes of base64 it took on the wire.
	Size int64

	// Disposition is "attachment" or "inline" as the part declared it, falling
	// back to how the part was classified when it declared nothing.
	Disposition string

	// ContentID is the Content-ID with its angle brackets removed, so it can be
	// compared against the cid: URLs in an HTML body. Empty when absent.
	ContentID string
}

// Message is one captured email: its SMTP envelope, the parsed headers and
// bodies, and the exact bytes we received.
type Message struct {
	// ID identifies the message within the running process. It is assigned by
	// the store when the message is added and is empty before that: a captured
	// message that is never stored has no identity to speak of.
	ID string

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

	// Headers is the message header block in the order it was written, with
	// repeated fields kept. It is what makes header inspection possible: the
	// fields above are a handful of headers a viewer needs by name, and every
	// other one — Received, Message-ID, Content-Transfer-Encoding, whatever the
	// sending library added — is only here.
	Headers []Header

	// Attachments describes the non-body MIME parts. Metadata only; see
	// Attachment.
	Attachments []Attachment

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

// Clone returns a copy of m that shares nothing mutable with it.
//
// It exists because a Message is handed across ownership boundaries — the SMTP
// session builds one, the store keeps one, a consumer reads one — and a plain
// struct copy is not enough: the copy's Raw, EnvelopeTo and HeaderTo would
// still point at the original's backing arrays, so writing through either one
// would be visible in the other.
//
// A new slice-backed field on Message needs a line here. The remaining fields
// need none: strings and time.Time are values, and ParseError holds an error
// built by this package that is never modified after Capture returns.
func (m Message) Clone() Message {
	// The value receiver has already made the shallow copy; what is left is to
	// give each slice field a backing array of its own. bytes.Clone and
	// slices.Clone return nil for a nil input, so an absent field stays absent
	// rather than becoming an empty non-nil slice.
	m.Raw = bytes.Clone(m.Raw)
	m.EnvelopeTo = slices.Clone(m.EnvelopeTo)
	m.HeaderTo = slices.Clone(m.HeaderTo)
	// Header and Attachment hold nothing but strings and an int64, so cloning
	// the slice is a complete copy of what it contains.
	m.Headers = slices.Clone(m.Headers)
	m.Attachments = slices.Clone(m.Attachments)
	return m
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

	m.Headers = headerFields(&mr.Header)
	m.Subject = subject(&mr.Header)
	m.HeaderFrom = strings.Join(addressList(&mr.Header, "From"), ", ")
	m.HeaderTo = addressList(&mr.Header, "To")

	if err := m.readBodies(mr); err != nil {
		return fmt.Errorf("parse mail body: %w", err)
	}

	return nil
}

// readBodies walks the MIME tree, collecting the text and HTML parts and
// recording metadata for everything else.
func (m *Message) readBodies(mr *mail.Reader) error {
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil && !gomessage.IsUnknownCharset(err) {
			return err
		}

		// go-message classifies a part as inline unless it is explicitly
		// dispositioned as an attachment, so "inline" here means "not declared
		// an attachment" rather than "is body text"; isBodyPart decides that.
		switch h := part.Header.(type) {
		case *mail.AttachmentHeader:
			err = m.readAttachment(&h.Header, "attachment", part.Body)
		case *mail.InlineHeader:
			if isBodyPart(&h.Header) {
				err = m.readBody(&h.Header, part.Body)
			} else {
				err = m.readAttachment(&h.Header, "inline", part.Body)
			}
		default:
			// A part shape we do not know. Drain it so the reader can move on
			// rather than silently returning it again.
			_, err = io.Copy(io.Discard, part.Body)
		}
		if err != nil {
			return err
		}
	}
}

// readBody appends a text or HTML part to the corresponding body.
func (m *Message) readBody(h *gomessage.Header, body io.Reader) error {
	content, err := io.ReadAll(body)
	if err != nil {
		return err
	}

	if contentType, _ := partContentType(h); contentType == "text/html" {
		m.HTMLBody = appendPart(m.HTMLBody, string(content))
		return nil
	}
	m.TextBody = appendPart(m.TextBody, string(content))
	return nil
}

// readAttachment records what the part says about itself and throws the payload
// away.
//
// The bytes are counted through io.Discard rather than buffered: the attachment
// view reports metadata, and holding a second copy of every attachment would
// double the memory a captured message costs for no gain. The encoded original
// is still in Raw.
//
// fallbackDisposition is how the part was classified when it declares no
// Content-Disposition of its own.
func (m *Message) readAttachment(h *gomessage.Header, fallbackDisposition string, body io.Reader) error {
	size, err := io.Copy(io.Discard, body)
	if err != nil {
		return err
	}

	contentType, ctParams := partContentType(h)

	disposition := fallbackDisposition
	dispParams := map[string]string(nil)
	if d, params, err := h.ContentDisposition(); err == nil {
		dispParams = params
		if d != "" {
			disposition = d
		}
	}

	m.Attachments = append(m.Attachments, Attachment{
		Filename:    partFilename(ctParams, dispParams),
		ContentType: contentType,
		Size:        size,
		Disposition: disposition,
		ContentID:   contentID(h),
	})
	return nil
}

// isBodyPart decides whether an inline part is the message text or something
// carried alongside it.
//
// The distinction matters because go-message hands back an InlineHeader for any
// part not marked as an attachment, which includes the embedded image an HTML
// mail refers to by cid:. Splicing that into TextBody would put binary in the
// preview and hide the image from the attachment list, so a part counts as body
// only when it is textual and claims no filename of its own.
func isBodyPart(h *gomessage.Header) bool {
	contentType, ctParams := partContentType(h)
	if contentType != "text/plain" && contentType != "text/html" {
		return false
	}

	dispParams := map[string]string(nil)
	if _, params, err := h.ContentDisposition(); err == nil {
		dispParams = params
	}
	return partFilename(ctParams, dispParams) == ""
}

// partContentType returns the part's media type and Content-Type parameters. A
// part with a missing or unreadable Content-Type is text/plain, which is both
// the RFC 2045 default and the most useful guess for a header we cannot read.
func partContentType(h *gomessage.Header) (string, map[string]string) {
	contentType, params, err := h.ContentType()
	if err != nil || contentType == "" {
		return "text/plain", params
	}
	return contentType, params
}

// partFilename returns the name the sender gave a part. The Content-Disposition
// filename wins over the Content-Type name, which RFC 2183 discourages but
// senders still emit. Both arrive already decoded from RFC 2047 / RFC 2231.
func partFilename(ctParams, dispParams map[string]string) string {
	if name := dispParams["filename"]; name != "" {
		return name
	}
	return ctParams["name"]
}

// contentID returns the part's Content-ID without its angle brackets, so it
// lines up with the cid: URLs an HTML body uses to refer to it.
func contentID(h *gomessage.Header) string {
	id := strings.TrimSpace(h.Get("Content-Id"))
	id = strings.TrimPrefix(id, "<")
	id = strings.TrimSuffix(id, ">")
	return id
}

// headerFields returns the header block in the order it was written.
//
// go-message already keeps the fields in document order and keeps repeats
// apart, so this is little more than a copy into a type the rest of the program
// can hold without depending on the parser.
func headerFields(h *mail.Header) []Header {
	fields := h.Fields()

	out := make([]Header, 0, fields.Len())
	for fields.Next() {
		out = append(out, Header{Key: headerKey(fields), Value: fields.Value()})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// headerKey recovers the field name as the sender spelled it.
//
// HeaderFields.Key canonicalises, turning MIME-Version into Mime-Version and
// Message-ID into Message-Id. That is right for lookups and wrong to show to
// someone inspecting what their mail library actually emitted, so the original
// is taken from the raw field and the canonical form is only a fallback.
func headerKey(f gomessage.HeaderFields) string {
	raw, err := f.Raw()
	if err == nil {
		if i := bytes.IndexByte(raw, ':'); i > 0 {
			return string(raw[:i])
		}
	}
	return f.Key()
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
