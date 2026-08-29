package cli

import (
	"flag"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"

	"github.com/igkougkousis01/mailtui/internal/smtp"
)

// The listener flags are defined once, here, and bound by every mode that
// takes them. Interactive mode and the script-mode commands then cannot drift
// apart in their defaults or in what they will accept, which is the whole
// reason this is a function and not three copies of three flag definitions.

// bindSMTP binds the flags that configure the catcher onto cfg, whose current
// values become the defaults. Callers start from smtp.DefaultConfig, so the
// defaults printed in help and the defaults used at runtime are the same
// numbers.
func bindSMTP(fs *flag.FlagSet, cfg *smtp.Config) {
	fs.StringVar(&cfg.Addr, "smtp-addr", cfg.Addr, "loopback `host:port` to catch mail on")
	fs.Var(&byteSize{n: &cfg.MaxMessageBytes}, "max-message-bytes", "largest message to accept, in bytes or with a unit (10MB)")
	fs.IntVar(&cfg.MaxRecipients, "max-recipients", cfg.MaxRecipients, "most recipients to accept in one message")
}

// validateSMTP checks a configuration before anything is bound or drawn, so
// that a mistake on the command line is a message on stderr rather than a
// catcher that quietly accepts nothing.
func validateSMTP(cfg smtp.Config) error {
	if err := checkLoopback(cfg.Addr); err != nil {
		return err
	}
	if cfg.MaxMessageBytes <= 0 {
		// Zero is not "no limit" here even though the SMTP library underneath
		// would read it that way. A catcher holds every message it accepts in
		// memory; an uncapped one is a way to lose the machine to a test that
		// sends the wrong file.
		return fmt.Errorf("--max-message-bytes must be positive, not %d", cfg.MaxMessageBytes)
	}
	if cfg.MaxRecipients <= 0 {
		// Zero would make the catcher refuse every RCPT TO, which is a catcher
		// that catches nothing: far more likely a typo than an intention.
		return fmt.Errorf("--max-recipients must be positive, not %d", cfg.MaxRecipients)
	}
	return nil
}

// checkLoopback rejects an address the catcher must not listen on.
//
// The catcher takes any mail offered to it, from anyone, with no
// authentication — which is exactly what makes it useful and exactly why it
// belongs on the loopback interface only. --smtp-addr exists to change the
// port, not to publish the catcher, so an address that would accept
// connections from the network is refused rather than trusted to be
// deliberate.
func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--smtp-addr %q is not a host:port address", addr)
	}

	if host == "" {
		return fmt.Errorf("--smtp-addr %q would listen on every interface; use a loopback address such as %s", addr, smtp.DefaultAddr)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("--smtp-addr %q is not a loopback address; the catcher accepts mail without authentication and must not be reachable from the network", addr)
}

// byteSize is a flag.Value for a size written either as a plain count of bytes
// or with a unit: 10485760, 10MB and 10MiB are the same value.
//
// It is thirty lines rather than a dependency because that is all it needs to
// be. Units are powers of 1024 — KB and KiB both mean 1024 bytes — which is
// the reading a developer capping a mail server means, and which is stated in
// the help rather than left to be guessed.
type byteSize struct{ n *int64 }

// units are longest-first, so that "MB" is not matched as "B" with "M" left
// over.
var units = []struct {
	suffix string
	scale  int64
}{
	{"KIB", 1 << 10}, {"MIB", 1 << 20}, {"GIB", 1 << 30},
	{"KB", 1 << 10}, {"MB", 1 << 20}, {"GB", 1 << 30},
	{"K", 1 << 10}, {"M", 1 << 20}, {"G", 1 << 30},
	{"B", 1},
}

func (b *byteSize) Set(s string) error {
	text := strings.ToUpper(strings.TrimSpace(s))
	if text == "" {
		return fmt.Errorf("not a size; write a number of bytes, or a number with a unit such as 10MB")
	}

	scale := int64(1)
	for _, u := range units {
		if rest, ok := strings.CutSuffix(text, u.suffix); ok && rest != "" {
			text, scale = strings.TrimSpace(rest), u.scale
			break
		}
	}

	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return fmt.Errorf("not a size; write a number of bytes, or a number with a unit such as 10MB")
	}
	// Checked rather than allowed to wrap: a silently negative cap would be
	// refused later by validateSMTP, but with a number the user never typed.
	if n != 0 && (n > math.MaxInt64/scale || n < math.MinInt64/scale) {
		return fmt.Errorf("too large a size to be a number of bytes")
	}

	*b.n = n * scale
	return nil
}

// String renders the size the way it would be written, which is what the
// default in a help line should look like.
func (b *byteSize) String() string {
	if b == nil || b.n == nil {
		return ""
	}
	return formatSize(*b.n)
}

// formatSize writes n with the largest unit that divides it exactly, so a
// default of 26214400 reads as 25MB. A size that is not a round multiple is
// left as bytes rather than rounded: a help line that rounds is a help line
// that lies.
func formatSize(n int64) string {
	for _, u := range []struct {
		suffix string
		scale  int64
	}{{"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}} {
		if n >= u.scale && n%u.scale == 0 {
			return strconv.FormatInt(n/u.scale, 10) + u.suffix
		}
	}
	return strconv.FormatInt(n, 10)
}
