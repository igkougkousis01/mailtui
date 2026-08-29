# Recording the terminal demo

Record the real application; do not mock the inbox or edit message content into a screenshot. Aim for one 20–30 second clip at roughly 100×28 terminal cells, with a readable font, a clean prompt, and no personal paths or tokens on screen.

## Prepare

Build a temporary demo binary outside the repository:

```bash
go build -o /tmp/mailtui-demo ./cmd/mailtui
```

In a second terminal, define a short `send-demo` function before recording. It uses only Python's standard library and sends a body, link, OTP, and small attachment:

```bash
send-demo() {
  python3 - <<'PY'
import smtplib
from email.message import EmailMessage

message = EmailMessage()
message["From"] = "app@example.test"
message["To"] = "user@example.test"
message["Subject"] = "Your sign-in code"
message.set_content("Your verification code is 482731.\n\nOpen https://example.test/verify?token=demo")
message.add_attachment(b"demo receipt\n", maintype="text", subtype="plain", filename="receipt.txt")

with smtplib.SMTP("127.0.0.1", 1025) as smtp:
    smtp.send_message(message)
PY
}
```

Test `send-demo` once before recording, then restart `mailtui` with an empty inbox.

## Shot 1: interactive inbox (about 15–20 seconds)

1. Start `/tmp/mailtui-demo` in the primary terminal.
2. Run `send-demo` in the second terminal.
3. Return immediately to the primary terminal as the message appears live.
4. Press `b`, `h`, `r`, and `a`, pausing briefly on each Body, Headers, Raw, and Attachments view.
5. End on Body or Attachments, whichever reads best at the chosen terminal size.

Keep the pointer out of frame and avoid excessive scrolling. The subject, OTP, link, and attachment name should be readable.

## Shot 2: script automation (about 5–8 seconds)

Quit the interactive inbox so it releases port 1025. In the primary terminal run:

```bash
/tmp/mailtui-demo extract otp --to user@example.test --timeout 10s
```

While it waits, run `send-demo` in the second terminal. Capture the single `482731` result on stdout. An equally good alternative is:

```bash
/tmp/mailtui-demo assert --to user@example.test --subject "sign-in" --contains "verification code" --timeout 10s
```

For `assert`, show the successful exit status because success intentionally prints nothing.

## Export and add the asset

- Prefer a cropped GIF or video that stays legible at the README content width.
- Keep the clip short and avoid lossy scaling that makes terminal text shimmer.
- Remove terminal chrome if it distracts, but do not splice or fabricate application states.
- Save the final GIF as `docs/assets/mailtui-demo.gif` (or a real still as `docs/assets/mailtui.png`).
- Replace the placeholder in `README.md` with the corresponding Markdown image.

Before committing, view the README on GitHub and confirm the asset loads, remains readable on a laptop display, and contains no local usernames, paths, notifications, or secrets.
