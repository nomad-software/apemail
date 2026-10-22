// Package autoconfig finds the settings of the servers of an email address,
// as other clients do: from a configuration file the provider of the domain
// publishes, from the public database of mail providers (the ISPDB),
// from the ISPDB entry of whoever handles the domain's mail, and from the SRV
// records of RFC 6186. Only the domain is sent anywhere but to the domain
// itself.
//
// It has no dependency on the UI.
package autoconfig

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nomad-software/apemail/internal/account"
)

// Config is the settings found for an address.
type Config struct {
	// Source says where they were found.
	Source   string
	Incoming account.Server
	// Outgoing has no Host if no SMTP server was found.
	Outgoing account.Server
}

// timeout limits how long each place is given to answer.
const timeout = 10 * time.Second

// maxSize limits how much of a configuration file is read.
const maxSize = 1 << 20

// ispdb is the address of the ISPDB, the public database of mail providers.
// Tests change it.
var ispdb = "https://autoconfig.thunderbird.net/v1.1/"

// client fetches the configuration files. Tests change it.
var client = &http.Client{Timeout: timeout}

// resolver looks up MX and SRV records. Tests change it.
var resolver interface {
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)
} = net.DefaultResolver

// source is a place to look for settings.
type source struct {
	name string
	find func(ctx context.Context) (*Config, error)
}

// Lookup finds the settings of the servers of the email address. The places
// are asked at the same time, and what the most trusted of those that
// answer says is returned.
func Lookup(ctx context.Context, email string) (*Config, error) {
	local, domain, ok := strings.Cut(strings.TrimSpace(email), "@")
	if !ok || local == "" || domain == "" || strings.ContainsAny(domain, "/?#@ ") {
		return nil, fmt.Errorf("%q is not an email address", email)
	}
	domain = strings.ToLower(domain)
	email = local + "@" + domain

	// The places are asked by goroutines that can outlive the lookup, so
	// they are given what to ask with now.
	f := finder{ispdb: ispdb, client: client, resolver: resolver}
	sources := []source{
		{"the provider of " + domain, func(ctx context.Context) (*Config, error) {
			u := "https://autoconfig." + domain + "/mail/config-v1.1.xml?emailaddress=" + url.QueryEscape(email)
			return f.fetch(ctx, u, email)
		}},
		{"the provider of " + domain, func(ctx context.Context) (*Config, error) {
			return f.fetch(ctx, "https://"+domain+"/.well-known/autoconfig/mail/config-v1.1.xml", email)
		}},
		{"the database of mail providers (ISPDB)", func(ctx context.Context) (*Config, error) {
			return f.fetch(ctx, f.ispdb+url.PathEscape(domain), email)
		}},
		{"the database of mail providers (ISPDB), for the server that handles the mail of " + domain, func(ctx context.Context) (*Config, error) {
			return f.fromMX(ctx, domain, email)
		}},
		{"the SRV records of " + domain, func(ctx context.Context) (*Config, error) {
			return f.fromSRV(ctx, domain, email)
		}},
	}

	ctx, cancel := context.WithTimeout(ctx, 2*timeout)
	defer cancel()

	type answer struct {
		config *Config
		err    error
	}
	answers := make([]chan answer, len(sources))
	for i, s := range sources {
		answers[i] = make(chan answer, 1)
		go func() {
			config, err := s.find(ctx)
			if config != nil {
				config.Source = s.name
			}
			answers[i] <- answer{config, err}
		}()
	}
	// The answers are taken in the order the places are trusted, so that a
	// quick one does not win over a better one.
	for i := range sources {
		if a := <-answers[i]; a.err == nil && a.config != nil {
			return a.config, nil
		}
	}
	return nil, fmt.Errorf("no settings were found for %s", domain)
}

// finder asks the places for settings.
type finder struct {
	ispdb    string
	client   *http.Client
	resolver interface {
		LookupMX(ctx context.Context, name string) ([]*net.MX, error)
		LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)
	}
}

// fetch reads a configuration file from the address.
func (f finder) fetch(ctx context.Context, u, email string) (*Config, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSize))
	if err != nil {
		return nil, err
	}
	return Parse(data, email)
}

// fromMX finds the domain of the server that handles the mail of the domain,
// and looks for it in the ISPDB, as a domain hosted by a provider is not in
// it under its own name.
func (f finder) fromMX(ctx context.Context, domain, email string) (*Config, error) {
	mxs, err := f.resolver.LookupMX(ctx, domain)
	if err != nil {
		return nil, err
	}
	if len(mxs) == 0 {
		return nil, errors.New("there are no MX records")
	}
	host := strings.ToLower(strings.TrimSuffix(mxs[0].Host, "."))
	labels := strings.Split(host, ".")
	// The last two labels are the domain of most providers, and the last
	// three of those under a country's second level, as co.uk.
	var failed []error
	for _, n := range []int{2, 3} {
		if len(labels) < n {
			break
		}
		base := strings.Join(labels[len(labels)-n:], ".")
		if base == domain {
			continue
		}
		config, err := f.fetch(ctx, f.ispdb+url.PathEscape(base), email)
		if err == nil {
			return config, nil
		}
		failed = append(failed, err)
	}
	return nil, errors.Join(failed...)
}

// fromSRV finds the servers from the SRV records of RFC 6186.
func (f finder) fromSRV(ctx context.Context, domain, email string) (*Config, error) {
	config := &Config{}
	found := false
	for _, s := range []struct {
		service  string
		security account.Security
	}{{"imaps", account.SecurityTLS}, {"imap", account.SecurityStartTLS}} {
		if host, port, ok := f.srv(ctx, s.service, domain); ok {
			config.Incoming = account.Server{Host: host, Port: port, Username: email, Security: s.security, Auth: account.AuthPassword}
			found = true
			break
		}
	}
	if !found {
		return nil, errors.New("there are no SRV records for IMAP")
	}
	for _, s := range []struct {
		service  string
		security account.Security
	}{{"submissions", account.SecurityTLS}, {"submission", account.SecurityStartTLS}} {
		if host, port, ok := f.srv(ctx, s.service, domain); ok {
			config.Outgoing = account.Server{Host: host, Port: port, Username: email, Security: s.security, Auth: account.AuthPassword}
			break
		}
	}
	return config, nil
}

// srv returns the target of the first SRV record of the service, unless it
// says there is no such service, as a target of "." does.
func (f finder) srv(ctx context.Context, service, domain string) (string, int, bool) {
	_, records, err := f.resolver.LookupSRV(ctx, service, "tcp", domain)
	if err != nil || len(records) == 0 {
		return "", 0, false
	}
	host := strings.TrimSuffix(records[0].Target, ".")
	if host == "" {
		return "", 0, false
	}
	return host, int(records[0].Port), true
}

// clientConfig is the XML of a configuration file, of which only the
// servers are read.
type clientConfig struct {
	Providers []struct {
		Incoming []server `xml:"incomingServer"`
		Outgoing []server `xml:"outgoingServer"`
	} `xml:"emailProvider"`
}

type server struct {
	Type           string   `xml:"type,attr"`
	Hostname       string   `xml:"hostname"`
	Port           string   `xml:"port"`
	SocketType     string   `xml:"socketType"`
	Username       string   `xml:"username"`
	Authentication []string `xml:"authentication"`
}

// Parse reads a configuration file, in the autoconfig format the ISPDB and
// providers publish, for the email address.
func Parse(data []byte, email string) (*Config, error) {
	var cc clientConfig
	if err := xml.Unmarshal(data, &cc); err != nil {
		return nil, fmt.Errorf("the configuration cannot be read: %w", err)
	}
	config := &Config{}
	found := false
	for _, p := range cc.Providers {
		if s, ok := best(p.Incoming, "imap", email); ok && !found {
			config.Incoming, found = s, true
		}
		if s, ok := best(p.Outgoing, "smtp", email); ok && config.Outgoing.Host == "" {
			config.Outgoing = s
		}
	}
	if !found {
		return nil, errors.New("the configuration has no IMAP server that can be used")
	}
	return config, nil
}

// best returns the server of the type that is the most secure of those with
// a way of logging in that is supported.
func best(servers []server, typ, email string) (account.Server, bool) {
	var chosen account.Server
	rank := -1
	for _, s := range servers {
		if !strings.EqualFold(s.Type, typ) {
			continue
		}
		port, err := strconv.Atoi(strings.TrimSpace(s.Port))
		if err != nil || port <= 0 || port > 65535 {
			continue
		}
		var security account.Security
		r := 0
		switch strings.ToUpper(strings.TrimSpace(s.SocketType)) {
		case "SSL":
			security, r = account.SecurityTLS, 2
		case "STARTTLS":
			security, r = account.SecurityStartTLS, 1
		case "PLAIN":
			security, r = account.SecurityNone, 0
		default:
			continue
		}
		auth, ok := authOf(s.Authentication)
		if !ok {
			continue
		}
		if r > rank {
			rank = r
			chosen = account.Server{
				Host:     expand(strings.TrimSpace(s.Hostname), email),
				Port:     port,
				Username: expand(strings.TrimSpace(s.Username), email),
				Security: security,
				Auth:     auth,
			}
		}
	}
	return chosen, rank >= 0 && chosen.Host != ""
}

// authOf returns the first of the ways of logging in that is supported.
func authOf(methods []string) (account.Auth, bool) {
	if len(methods) == 0 {
		return account.AuthPassword, true
	}
	for _, m := range methods {
		switch strings.TrimSpace(m) {
		case "password-cleartext", "plain":
			return account.AuthPassword, true
		case "password-encrypted", "secure":
			return account.AuthEncryptedPassword, true
		case "client-IP-address", "none":
			return account.AuthPassword, true
		case "TLS-client-cert":
			return account.AuthTLSCert, true
		}
	}
	return "", false
}

// expand replaces the placeholders of a configuration file with the parts of
// the email address.
func expand(s, email string) string {
	local, domain, _ := strings.Cut(email, "@")
	return strings.NewReplacer(
		"%EMAILADDRESS%", email,
		"%EMAILLOCALPART%", local,
		"%EMAILDOMAIN%", domain,
	).Replace(s)
}
