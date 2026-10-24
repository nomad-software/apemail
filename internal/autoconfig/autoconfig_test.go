package autoconfig

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nomad-software/apemail/internal/account"
)

const sample = `<?xml version="1.0"?>
<clientConfig version="1.1">
  <emailProvider id="example.com">
    <domain>example.com</domain>
    <incomingServer type="pop3">
      <hostname>pop.example.com</hostname>
      <port>995</port>
      <socketType>SSL</socketType>
      <authentication>password-cleartext</authentication>
      <username>%EMAILADDRESS%</username>
    </incomingServer>
    <incomingServer type="imap">
      <hostname>imap.%EMAILDOMAIN%</hostname>
      <port>143</port>
      <socketType>STARTTLS</socketType>
      <authentication>password-encrypted</authentication>
      <username>%EMAILLOCALPART%</username>
    </incomingServer>
    <incomingServer type="imap">
      <hostname>imap.example.com</hostname>
      <port>993</port>
      <socketType>SSL</socketType>
      <authentication>OAuth2</authentication>
      <authentication>password-cleartext</authentication>
      <username>%EMAILADDRESS%</username>
    </incomingServer>
    <outgoingServer type="smtp">
      <hostname>smtp.example.com</hostname>
      <port>465</port>
      <socketType>SSL</socketType>
      <authentication>password-cleartext</authentication>
      <username>%EMAILADDRESS%</username>
    </outgoingServer>
  </emailProvider>
</clientConfig>`

func TestParse(t *testing.T) {
	c, err := Parse([]byte(sample), "me@example.com")
	if err != nil {
		t.Fatal(err)
	}
	want := account.Server{Host: "imap.example.com", Port: 993, Username: "me@example.com", Security: account.SecurityTLS, Auth: account.AuthPassword}
	if c.Incoming != want {
		t.Errorf("incoming = %+v", c.Incoming)
	}
	if c.Outgoing.Host != "smtp.example.com" || c.Outgoing.Port != 465 || c.Outgoing.Security != account.SecurityTLS {
		t.Errorf("outgoing = %+v", c.Outgoing)
	}
	if _, err := Parse([]byte(`<clientConfig/>`), "me@example.com"); err == nil {
		t.Error("an empty configuration was accepted")
	}
}

type fakeResolver struct {
	mx  map[string]string
	srv map[string]net.SRV
}

func (r fakeResolver) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	if host, ok := r.mx[name]; ok {
		return []*net.MX{{Host: host}}, nil
	}
	return nil, errors.New("no MX")
}

func (r fakeResolver) LookupSRV(_ context.Context, service, proto, name string) (string, []*net.SRV, error) {
	if s, ok := r.srv[service+"."+name]; ok {
		return "", []*net.SRV{&s}, nil
	}
	return "", nil, errors.New("no SRV")
}

// ispdbDelay is how long the ISPDB of the test takes to answer.
var ispdbDelay time.Duration

// fake has the lookups reach only a server of the test, which has a
// configuration for the domains given.
func fake(t *testing.T, domains map[string]string, r fakeResolver) {
	delay := ispdbDelay
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		time.Sleep(delay)
		domain := strings.TrimPrefix(req.URL.Path, "/ispdb/")
		if body, ok := domains[domain]; ok {
			w.Write([]byte(body))
			return
		}
		http.NotFound(w, req)
	}))
	t.Cleanup(srv.Close)
	oldISPDB, oldClient, oldResolver := ispdb, client, resolver
	ispdb = srv.URL + "/ispdb/"
	// What is not the ISPDB, as autoconfig.<domain>, cannot be reached.
	client = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
		},
	}}
	client.Transport.(*http.Transport).Proxy = nil
	resolver = r
	t.Cleanup(func() { ispdb, client, resolver = oldISPDB, oldClient, oldResolver })
}

func TestLookupISPDB(t *testing.T) {
	fake(t, map[string]string{"example.com": sample}, fakeResolver{})
	c, err := Lookup(context.Background(), "me@Example.com")
	if err != nil {
		t.Fatal(err)
	}
	if c.Incoming.Host != "imap.example.com" || !strings.Contains(c.Source, "ISPDB") {
		t.Errorf("config = %+v", c)
	}
}

func TestLookupMX(t *testing.T) {
	fake(t, map[string]string{"example.com": sample}, fakeResolver{mx: map[string]string{"mydomain.org": "mx1.mail.example.com."}})
	c, err := Lookup(context.Background(), "me@mydomain.org")
	if err != nil {
		t.Fatal(err)
	}
	if c.Incoming.Host != "imap.example.com" || c.Incoming.Username != "me@mydomain.org" {
		t.Errorf("config = %+v", c)
	}
}

func TestLookupSRV(t *testing.T) {
	fake(t, nil, fakeResolver{srv: map[string]net.SRV{
		"imaps.mydomain.org":      {Target: "mail.mydomain.org.", Port: 993},
		"submission.mydomain.org": {Target: "mail.mydomain.org.", Port: 587},
	}})
	c, err := Lookup(context.Background(), "me@mydomain.org")
	if err != nil {
		t.Fatal(err)
	}
	if c.Incoming.Host != "mail.mydomain.org" || c.Incoming.Port != 993 || c.Incoming.Security != account.SecurityTLS {
		t.Errorf("incoming = %+v", c.Incoming)
	}
	if c.Outgoing.Port != 587 || c.Outgoing.Security != account.SecurityStartTLS {
		t.Errorf("outgoing = %+v", c.Outgoing)
	}
}

func TestLookupNothing(t *testing.T) {
	fake(t, nil, fakeResolver{})
	if _, err := Lookup(context.Background(), "me@nowhere.invalid"); err == nil {
		t.Error("settings were found")
	}
	if _, err := Lookup(context.Background(), "not an address"); err == nil {
		t.Error("an address was accepted")
	}
}

// TestLookupTrustedWins checks that every place is asked at once, and that
// the answer of one that is quick does not win over that of one that is
// trusted more and slower: the ISPDB over the SRV records.
func TestLookupTrustedWins(t *testing.T) {
	ispdbDelay = 200 * time.Millisecond
	defer func() { ispdbDelay = 0 }()
	fake(t, map[string]string{"example.com": sample}, fakeResolver{srv: map[string]net.SRV{
		"imaps.example.com": {Target: "srv.example.com.", Port: 993},
	}})
	c, err := Lookup(context.Background(), "me@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if c.Incoming.Host != "imap.example.com" || !strings.Contains(c.Source, "ISPDB") {
		t.Errorf("config = %+v", c)
	}
}
