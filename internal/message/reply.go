package message

import (
	"html"
	"strings"

	"github.com/emersion/go-message/mail"
)

// ReplySubject returns the subject of a reply to a message with the subject.
func ReplySubject(subject string) string {
	if hasPrefixFold(strings.TrimSpace(subject), "re:") {
		return subject
	}
	return "Re: " + subject
}

// ForwardSubject returns the subject of a message forwarding one with the
// subject.
func ForwardSubject(subject string) string {
	s := strings.TrimSpace(subject)
	if hasPrefixFold(s, "fwd:") || hasPrefixFold(s, "fw:") {
		return subject
	}
	return "Fwd: " + subject
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// ReplyRecipients returns who a reply to the message goes to: whoever it
// asks replies to go to, or its sender; and, when replying to all, everyone
// else it was sent to as copies. The addresses of self are left out.
func (m *Message) ReplyRecipients(all bool, self ...string) (to, cc string) {
	mine := func(a *mail.Address) bool {
		for _, s := range self {
			if s != "" && strings.EqualFold(a.Address, s) {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	pick := func(list []*mail.Address) []*mail.Address {
		var kept []*mail.Address
		for _, a := range list {
			key := strings.ToLower(a.Address)
			if a.Address == "" || seen[key] {
				continue
			}
			seen[key] = true
			kept = append(kept, a)
		}
		return kept
	}

	first := m.ReplyTo
	if len(first) == 0 {
		first = m.From
	}
	// A reply to a message one sent oneself goes to whoever it was sent to.
	if len(first) > 0 && mine(first[0]) && len(m.To) > 0 {
		first = m.To
	}
	toList := pick(first)
	if !all {
		return FormatAddresses(toList), ""
	}

	var others []*mail.Address
	for _, a := range append(append([]*mail.Address{}, m.To...), m.Cc...) {
		if !mine(a) {
			others = append(others, a)
		}
	}
	return FormatAddresses(toList), FormatAddresses(pick(others))
}

// ReplyReferences returns the IDs a reply to the message refers to.
func (m *Message) ReplyReferences() (inReplyTo string, references []string) {
	references = append(references, m.References...)
	if m.MessageID != "" {
		references = append(references, m.MessageID)
	}
	return m.MessageID, references
}

// Attribution returns the line which says whose words a quote is.
func (m *Message) Attribution() string {
	who := FormatAddresses(m.From)
	if who == "" {
		who = "someone"
	}
	if m.Date.IsZero() {
		return who + " wrote:"
	}
	return "On " + m.Date.Local().Format("02/01/2006 15:04") + ", " + who + " wrote:"
}

// QuoteText returns the text with each line quoted.
func QuoteText(text string) string {
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, ">") {
			lines[i] = ">" + l
		} else {
			lines[i] = "> " + l
		}
	}
	return strings.Join(lines, "\n")
}

// QuoteHTML returns the message quoted, as HTML.
func (m *Message) QuoteHTML() string {
	body := m.HTML
	if body == "" {
		body = TextToHTML(m.Body())
	}
	return "<p>" + html.EscapeString(m.Attribution()) + "</p><blockquote type=\"cite\">" + body + "</blockquote>"
}

// ForwardHeader returns the lines which say what message is forwarded.
func (m *Message) ForwardHeader() string {
	var b strings.Builder
	b.WriteString("-------- Forwarded Message --------\n")
	b.WriteString("Subject: " + m.Subject + "\n")
	if !m.Date.IsZero() {
		b.WriteString("Date: " + m.Date.Local().Format("Mon, 02 Jan 2006 15:04:05 -0700") + "\n")
	}
	b.WriteString("From: " + FormatAddresses(m.From) + "\n")
	b.WriteString("To: " + FormatAddresses(m.To) + "\n")
	if len(m.Cc) > 0 {
		b.WriteString("Cc: " + FormatAddresses(m.Cc) + "\n")
	}
	return b.String()
}

// TextToHTML returns plain text as HTML that shows it as it is.
func TextToHTML(text string) string {
	escaped := html.EscapeString(text)
	return "<div style=\"white-space: pre-wrap\">" + strings.ReplaceAll(escaped, "\n", "<br>") + "</div>"
}
