package cli

import (
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/igkougkousis01/mailtui/internal/smtp"
)

// parseSMTP runs args through a flag set carrying only the catcher flags, the
// way every mode binds them.
func parseSMTP(t *testing.T, args ...string) (smtp.Config, error) {
	t.Helper()

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	cfg := smtp.DefaultConfig()
	bindSMTP(fs, &cfg)

	return cfg, fs.Parse(args)
}

// TestCatcherDefaults pins the values a plain run uses. They are the numbers
// the help quotes and the numbers the tests below vary from, so a change to
// any of them should be a deliberate edit here.
func TestCatcherDefaults(t *testing.T) {
	cfg, err := parseSMTP(t)
	if err != nil {
		t.Fatalf("parsing no flags: %v", err)
	}

	if cfg != smtp.DefaultConfig() {
		t.Errorf("config with no flags = %+v, want %+v", cfg, smtp.DefaultConfig())
	}
	if cfg.Addr != "127.0.0.1:1025" {
		t.Errorf("default address = %q, want the loopback SMTP port", cfg.Addr)
	}
	if cfg.MaxMessageBytes != 25*1024*1024 {
		t.Errorf("default max message = %d, want 25MB", cfg.MaxMessageBytes)
	}
	if cfg.MaxRecipients != 100 {
		t.Errorf("default max recipients = %d, want 100", cfg.MaxRecipients)
	}
}

// TestScriptAndInteractiveShareTheSameDefaults is the guarantee that a command
// and the inbox catch mail the same way. They bind through the same function
// for exactly this reason; the test is here so that a future change that
// splits them fails rather than surprises someone whose test passes
// interactively and not in CI.
func TestScriptAndInteractiveShareTheSameDefaults(t *testing.T) {
	interactiveFS := flag.NewFlagSet("mailtui", flag.ContinueOnError)
	interactiveCfg := smtp.DefaultConfig()
	bindSMTP(interactiveFS, &interactiveCfg)
	if err := interactiveFS.Parse(nil); err != nil {
		t.Fatalf("parsing interactive flags: %v", err)
	}

	scriptFS := flag.NewFlagSet("wait", flag.ContinueOnError)
	var o options
	bindCommon(scriptFS, &o)
	if err := scriptFS.Parse(nil); err != nil {
		t.Fatalf("parsing script flags: %v", err)
	}

	if o.smtp != interactiveCfg {
		t.Errorf("script mode defaults to %+v, interactive mode to %+v", o.smtp, interactiveCfg)
	}
	if o.timeout != defaultTimeout {
		t.Errorf("default timeout = %s, want %s", o.timeout, defaultTimeout)
	}
}

func TestCustomCatcherFlags(t *testing.T) {
	cfg, err := parseSMTP(t,
		"--smtp-addr", "127.0.0.1:2525",
		"--max-message-bytes", "10MB",
		"--max-recipients", "50")
	if err != nil {
		t.Fatalf("parsing custom flags: %v", err)
	}

	want := smtp.Config{Addr: "127.0.0.1:2525", MaxMessageBytes: 10 * 1024 * 1024, MaxRecipients: 50}
	if cfg != want {
		t.Errorf("config = %+v, want %+v", cfg, want)
	}
}

// TestSizeSyntax covers what --max-message-bytes accepts and what it refuses.
// The units are powers of 1024, which the help says and this pins.
func TestSizeSyntax(t *testing.T) {
	good := []struct {
		in   string
		want int64
	}{
		{"1", 1},
		{"10485760", 10 * 1024 * 1024},
		{"512KB", 512 * 1024},
		{"512kb", 512 * 1024},
		{"512KiB", 512 * 1024},
		{"10MB", 10 * 1024 * 1024},
		{"10 MB", 10 * 1024 * 1024},
		{"1GB", 1024 * 1024 * 1024},
		{"2048B", 2048},
		{"4M", 4 * 1024 * 1024},
		{"0", 0}, // parses; refused later, with a message about the value
	}
	for _, tt := range good {
		t.Run(tt.in, func(t *testing.T) {
			cfg, err := parseSMTP(t, "--max-message-bytes", tt.in)
			if err != nil {
				t.Fatalf("--max-message-bytes %q: %v", tt.in, err)
			}
			if cfg.MaxMessageBytes != tt.want {
				t.Errorf("--max-message-bytes %q = %d, want %d", tt.in, cfg.MaxMessageBytes, tt.want)
			}
		})
	}

	bad := []string{"", "MB", "ten", "10 megabytes", "1.5MB", "10MB extra", "9223372036854775807MB"}
	for _, in := range bad {
		t.Run("rejects "+in, func(t *testing.T) {
			if _, err := parseSMTP(t, "--max-message-bytes", in); err == nil {
				t.Errorf("--max-message-bytes %q was accepted, want a parse error", in)
			}
		})
	}
}

// TestSizeDoesNotOverflow: a size that would wrap past the top of an int64 is
// refused rather than silently becoming a small or negative cap.
func TestSizeDoesNotOverflow(t *testing.T) {
	for _, in := range []string{"9223372036854775807KB", "8796093022208MB", "-9223372036854775808KB"} {
		cfg, err := parseSMTP(t, "--max-message-bytes", in)
		if err == nil {
			t.Errorf("--max-message-bytes %q was accepted as %d, want it refused", in, cfg.MaxMessageBytes)
		}
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{25 * 1024 * 1024, "25MB"},
		{512 * 1024, "512KB"},
		{1024 * 1024 * 1024, "1GB"},
		{1, "1"},
		{1000, "1000"}, // not a round multiple: left as bytes rather than rounded
		{0, "0"},
	}
	for _, tt := range tests {
		if got := formatSize(tt.in); got != tt.want {
			t.Errorf("formatSize(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestValidateSMTP is the fail-fast check: everything here is refused before a
// port is claimed or a screen is drawn.
func TestValidateSMTP(t *testing.T) {
	tests := []struct {
		name string
		cfg  smtp.Config
		want string // "" means it should be accepted
	}{
		{"the defaults", smtp.DefaultConfig(), ""},
		{"another loopback port", cfgWith(func(c *smtp.Config) { c.Addr = "127.0.0.1:2525" }), ""},
		{"localhost by name", cfgWith(func(c *smtp.Config) { c.Addr = "localhost:2525" }), ""},
		{"another loopback address", cfgWith(func(c *smtp.Config) { c.Addr = "127.0.0.2:2525" }), ""},
		{"IPv6 loopback", cfgWith(func(c *smtp.Config) { c.Addr = "[::1]:2525" }), ""},
		{"port zero, for a test that wants any port", cfgWith(func(c *smtp.Config) { c.Addr = "127.0.0.1:0" }), ""},

		{"every interface", cfgWith(func(c *smtp.Config) { c.Addr = ":1025" }), "every interface"},
		{"a routable address", cfgWith(func(c *smtp.Config) { c.Addr = "0.0.0.0:1025" }), "not a loopback"},
		{"a LAN address", cfgWith(func(c *smtp.Config) { c.Addr = "192.168.1.10:1025" }), "not a loopback"},
		{"no port at all", cfgWith(func(c *smtp.Config) { c.Addr = "127.0.0.1" }), "not a host:port"},

		{"a zero message cap", cfgWith(func(c *smtp.Config) { c.MaxMessageBytes = 0 }), "--max-message-bytes must be positive"},
		{"a negative message cap", cfgWith(func(c *smtp.Config) { c.MaxMessageBytes = -1 }), "--max-message-bytes must be positive"},
		{"zero recipients", cfgWith(func(c *smtp.Config) { c.MaxRecipients = 0 }), "--max-recipients must be positive"},
		{"negative recipients", cfgWith(func(c *smtp.Config) { c.MaxRecipients = -5 }), "--max-recipients must be positive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSMTP(tt.cfg)

			if tt.want == "" {
				if err != nil {
					t.Fatalf("validateSMTP(%+v) = %v, want it accepted", tt.cfg, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateSMTP(%+v) accepted it, want an error mentioning %q", tt.cfg, tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("validateSMTP(%+v) = %q, want it to mention %q", tt.cfg, err, tt.want)
			}
		})
	}
}

// cfgWith is the defaults with one thing changed, so that each case above
// says only what it is about.
func cfgWith(change func(*smtp.Config)) smtp.Config {
	cfg := smtp.DefaultConfig()
	change(&cfg)
	return cfg
}
