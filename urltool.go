// Command urltool manipulates URLs on the command line and prints the updated URL.
// See its usage (-h, -help) for more information.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
)

func usage() {
	fmt.Fprint(os.Stderr, `Usage: urltool [-h|-help] [URL...] [MODIFIERS]

Modify one or more URLs and print the results.

Options:
  -h, -help
    Print this help text.

Modifiers:
  -nh
    Disable URL parsing hacks (domain.tld and user:bar@domain.tld parsing).
  -s SCHEME
    Set the URL's scheme.
  -o OPAQUE
    set the URL's opaque value.
  -u USER
    Set the URL's username.
  -pw PASSWD
    Set the URL's password.
  -U[=true|false]
    Strip user info from the URL.
  -H HOST
    Set the URL's host.
  -P PORT
    Change the URL's host port (after taking the host from -H).
  -p PATH
    Set the URL's path (or join to it).
  -j[=true|false]
    Force joining the URL's path instead of setting it when relative.
  -fq[=true|false]
    Force a ? to appear in the URL.
  -sq[=true|false]
    Strip query string before appending to it.
  -q K=V
    Append a ?K=V value to the query string. May be repeated. If no '='
    is found, an empty ?K= is added.
  -f
    Set the URL's #fragment.
  -r
    Parse a URL relative to the input URL and use the result (after all
    other modifiers).
`)
	os.Exit(2)
}

func main() {
	code := 0
	defer func() {
		if rc := recover(); rc != nil {
			panic(rc)
		}
		os.Exit(code)
	}()

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	if isTTY() {
		defer func() { _, _ = out.WriteString("\n") }()
	}

	argv := os.Args[1:]
	if len(argv) == 0 {
		usage()
	}

	newline := ""
	for len(argv) > 0 {
		urls, rest, err := parseArgs(argv)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}

		for _, u := range urls {
			_, _ = out.WriteString(newline)
			newline = "\n"
			_, _ = out.WriteString(u.String())
		}

		argv = rest
	}
}

func parseArgs(args []string) (urls []*url.URL, rest []string, err error) {
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		us := args[0]
		u, err := url.Parse(us)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to parse URL %q: %v", us, err)
		}
		urls = append(urls, u)
		args = args[1:]
	}

	var (
		nohacks       bool
		scheme        SetString
		opaque        SetString
		username      SetString
		password      SetString
		stripUser     bool
		host          SetString
		port          SetString
		newPath       SetString
		joinPath      bool
		forceQuery    bool
		stripQuery    bool
		query         queryArgs
		fragment      SetString
		parseRelative SetString
	)

	f := flag.NewFlagSet("urltool", flag.ExitOnError)
	f.Usage = usage
	f.BoolVar(&nohacks, "nh", false, "disable URL parsing hacks (domain.tld and user:bar@domain.tld parsing)")
	f.Var(&scheme, "s", "set the URL's scheme")
	f.Var(&opaque, "o", "set the URL's opaque value")
	f.Var(&username, "u", "set the URL's username")
	f.Var(&password, "pw", "set the URL's password")
	f.BoolVar(&stripUser, "U", false, "strip user info from the URL")
	f.Var(&host, "H", "set the URL's host")
	f.Var(&port, "P", "change the URL's host port (after taking the host from -H)")
	f.Var(&newPath, "p", "set the URL's path (or join to it)")
	f.BoolVar(&joinPath, "j", false, "force joining the URL's path instead of setting it when relative")
	f.BoolVar(&forceQuery, "fq", false, "force a ? to appear in the URL")
	f.BoolVar(&stripQuery, "sq", false, "strip query string before appending to it")
	f.Var(&query, "q", "append a ?K=V value to the query string")
	f.Var(&fragment, "f", "set the URL's #fragment")
	f.Var(&parseRelative, "r", "parse a `URL` relative to the input URL and use the result")
	if err := f.Parse(args); err != nil {
		return nil, nil, err
	}

	// Wait until here to check how many URLs there are, since the user might be passing -h or
	// -help.
	if len(urls) == 0 {
		return nil, nil, errors.New("no URLs given")
	}

	for i, u := range urls {
		if nohacks {
			// Skip the following URL hacks
		} else if at := strings.IndexByte(u.Opaque, '@'); u.Scheme != "" && at != -1 && u.Host == "" && u.Path == "" && u.User == nil {
			// Try to account for user:pass@domain style URLs
			user := u.Scheme
			pass := u.Opaque[:at]
			u.Host = u.Opaque[at+1:]
			if sep := strings.IndexByte(u.Host, '/'); sep != -1 {
				u.Host, u.Path = u.Host[:sep], u.Host[sep:]
			}
			u.User = url.UserPassword(user, pass)
			u.Opaque = ""
		} else if u.Scheme == "" && u.Host == "" && u.Path != "" {
			// Try to account for "foobar.com" as a URL, since that's technically a path
			if sep := strings.IndexByte(u.Path, '/'); sep != -1 {
				u.Host, u.Path = u.Path[:sep], u.Path[sep:] // Keep leading slash
			} else {
				u.Host, u.Path = u.Path, ""
			}
		}

		// Scheme
		if scheme.IsSet {
			u.Scheme = scheme.Str
		}

		// Opaque
		if opaque.IsSet {
			u.Opaque = opaque.Str
		}

		// User / password
		if stripUser {
			u.User = nil
		}
		user := u.User.Username()    // nil-safe
		pass, _ := u.User.Password() // nil-safe
		if username.IsSet {
			user = username.Str
		}
		if password.IsSet {
			pass = password.Str
		}
		if user != "" || pass != "" {
			u.User = url.UserPassword(user, pass)
		}

		// Hostname
		if host.IsSet {
			u.Host = host.Str
		}

		// Host port
		if port.IsSet {
			if _, err := strconv.ParseUint(port.Str, 10, 64); err != nil {
				return nil, nil, fmt.Errorf("invalid port number %q", port.Str)
			}
			h, _, err := net.SplitHostPort(u.Host)
			if err != nil {
				h = u.Host
			}
			u.Host = net.JoinHostPort(h, port.Str)
		}

		// Path
		if newPath.IsSet {
			if joinPath || !path.IsAbs(newPath.Str) {
				if u.Path == "" {
					u.Path = "/"
				}
				u.Path = path.Join(u.Path, newPath.Str)
			} else {
				u.Path = newPath.Str
			}

			u.Path = path.Clean(u.Path)
			if strings.HasPrefix(u.Path, "/../") {
				u.Path = "/"
			}
		}

		// Query string
		u.ForceQuery = forceQuery
		if stripQuery {
			u.RawQuery = ""
		}
		q := u.Query()
		for k, v := range query {
			q[k] = append(q[k], v...)
		}
		if len(q) != 0 {
			u.RawQuery = q.Encode()
		}

		// Fragment
		if fragment.IsSet {
			u.Fragment = fragment.Str
		}

		// Relative URL
		if parseRelative.IsSet {
			r, err := u.Parse(parseRelative.Str)
			if err != nil {
				return nil, nil, fmt.Errorf("unable to parse %q relative to %q: %v", r, u, err)
			}
			u = r
		}

		urls[i] = u
	}

	return urls, f.Args(), nil
}

type queryArgs url.Values

func (q *queryArgs) Set(s string) error {
	if *q == nil {
		*q = queryArgs{}
	}
	m := *q
	eq := strings.IndexByte(s, '=')
	if eq == -1 {
		m[s] = append(m[s], "")
		return nil
	}
	k, v := s[:eq], s[eq+1:]
	m[k] = append(m[k], v)
	return nil
}

func (q queryArgs) String() string {
	return "?K=V"
}

type SetString struct {
	IsSet bool
	Str   string
}

func (s *SetString) Set(v string) error {
	s.IsSet, s.Str = true, v
	return nil
}

func (s SetString) String() string {
	return s.Str
}

// isTTY attempts to determine whether the current stdout refers to a terminal.
func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false // Assume it's not a TTY
	}
	return (fi.Mode() & os.ModeNamedPipe) != os.ModeNamedPipe
}
