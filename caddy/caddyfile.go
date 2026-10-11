package weircaddy

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

// The Caddy module registration exception to the no-init rule (CLAUDE.md).
func init() {
	httpcaddyfile.RegisterHandlerDirective("weir", parseCaddyfile)
	// 08 §1: weir runs before reverse_proxy without a global order option.
	// EXPERIMENTAL in Caddy; the fallback is documenting `order`.
	httpcaddyfile.RegisterDirectiveOrder("weir", httpcaddyfile.Before, "reverse_proxy")
}

func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var m Handler
	if err := m.UnmarshalCaddyfile(h.Dispenser); err != nil {
		return nil, err
	}
	return &m, nil
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler. Syntax (08 §2):
//
//	weir {
//	    name <name>
//	    max_bytes <size>
//	    snapshot_dir <dir>
//	    multi_host
//	    store valkey {
//	        addrs <host:port>...; username|password|prefix|hash_tag <v>
//	        tls; cluster; co_locate_entries; no_clock_skew; skip_policy_check
//	        max_retention|max_clock_skew|call_timeout|hard_epoch_wait <duration>; max_hard_epochs <n>
//	    }
//	    key { query_drop|query_keep|headers|cookies|accept_encoding <v>...; query_sort; normalize_path }
//	    forward { allow <header>... }
//	    bypass { cookies|headers <v>... }
//	    limiter { max_concurrent|max_queue|max_per_partition <n>; max_queue_wait <duration> }
//	    stale { while_revalidate|if_error <duration> }
//	    experiments {
//	        cookie|header <name>; max_active <n>; signing_keys <key>...; downstream_keys_on_variant
//	        consent cookie <name> <value>
//	        experiment <name> { match [host <h>] [path <p>]; salt <s>; variants <name>=<weight>...; winner <name> }
//	    }
//	}
//
// Every error names the Caddyfile line (the dispenser adds it).
func (h *Handler) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	d.Next() // directive name
	file, line := d.File(), d.Line()
	if d.CountRemainingArgs() > 0 {
		return argErr(d) // the block is the only form; a shorthand would hide keys
	}
	seen := map[string]bool{}
	nameFile, nameLine := file, line
	var storeFile string
	var storeLine int
	for d.NextBlock(0) {
		key := d.Val()
		if key == "name" {
			nameFile, nameLine = d.File(), d.Line()
		}
		if key == "store" {
			storeFile, storeLine = d.File(), d.Line()
		}
		if err := h.blockKey(d, key, seen); err != nil {
			return err
		}
	}
	if err := validateName(h.Name); err != nil {
		return fmt.Errorf("%w, at %s:%d", err, nameFile, nameLine)
	}
	// Checked here as well as in Provision so `caddy adapt` reports a bad
	// store block with its line.
	if h.Store != nil {
		if err := h.Validate(); err != nil {
			return fmt.Errorf("%w, at %s:%d", err, storeFile, storeLine)
		}
	}
	return nil
}

func (h *Handler) blockKey(d *caddyfile.Dispenser, key string, seen map[string]bool) error {
	if seen[key] {
		return d.Errf("%q is set twice", key)
	}
	seen[key] = true
	switch key {
	case "name":
		return oneArg(d, &h.Name)
	case "snapshot_dir":
		return oneArg(d, &h.SnapshotDir)
	case "multi_host":
		return flag(d, &h.MultiHost)
	case "max_bytes":
		var s string
		if err := oneArg(d, &s); err != nil {
			return err
		}
		n, err := parseByteSize(s)
		if err != nil {
			return d.Errf("max_bytes: %v", err)
		}
		h.MaxBytes = ByteSize(n)
		return nil
	case "store":
		return h.storeBlock(d)
	case "key":
		return subBlock(d, "key", map[string]func() error{
			"query_drop":      func() error { return listArg(d, &h.Key.QueryDrop) },
			"query_keep":      func() error { return listArg(d, &h.Key.QueryKeep) },
			"query_sort":      func() error { return flag(d, &h.Key.QuerySort) },
			"normalize_path":  func() error { return flag(d, &h.Key.NormalizePath) },
			"headers":         func() error { return listArg(d, &h.Key.Headers) },
			"cookies":         func() error { return listArg(d, &h.Key.Cookies) },
			"accept_encoding": func() error { return listArg(d, &h.Key.AcceptEncoding) },
		})
	case "forward":
		return subBlock(d, "forward", map[string]func() error{
			"allow": func() error { return listArg(d, &h.Forward.Allow) },
		})
	case "bypass":
		return subBlock(d, "bypass", map[string]func() error{
			"cookies": func() error { return listArg(d, &h.Bypass.Cookies) },
			"headers": func() error { return listArg(d, &h.Bypass.Headers) },
		})
	case "limiter":
		return subBlock(d, "limiter", map[string]func() error{
			"max_concurrent":    func() error { return intArg(d, &h.Limiter.MaxConcurrent) },
			"max_queue":         func() error { return intArg(d, &h.Limiter.MaxQueue) },
			"max_queue_wait":    func() error { return durArg(d, &h.Limiter.MaxQueueWait) },
			"max_per_partition": func() error { return intArg(d, &h.Limiter.MaxPerPartition) },
		})
	case "experiments":
		return experimentsBlock(d, h)
	case "stale":
		return subBlock(d, "stale", map[string]func() error{
			"while_revalidate": func() error { return durArg(d, &h.Stale.WhileRevalidate) },
			"if_error":         func() error { return durArg(d, &h.Stale.IfError) },
		})
	}
	return d.Errf("unknown key %s", tok(key))
}

// storeBlock parses `store valkey { ... }`. The type is an argument so a
// second store kind can follow without breaking existing files.
func (h *Handler) storeBlock(d *caddyfile.Dispenser) error {
	if !d.NextArg() {
		return argErr(d)
	}
	if typ := d.Val(); typ != "valkey" {
		return d.Errf("unknown store type %s (only \"valkey\")", tok(typ))
	}
	s := &StoreConfig{Type: "valkey"}
	err := subBlock(d, "store", map[string]func() error{
		"addrs":             func() error { return listArg(d, &s.Addrs) },
		"username":          func() error { return secretArg(d, &s.Username) },
		"password":          func() error { return secretArg(d, &s.Password) },
		"tls":               func() error { return quietFlag(d, &s.TLS) },
		"cluster":           func() error { return quietFlag(d, &s.Cluster) },
		"prefix":            func() error { return oneArg(d, &s.Prefix) },
		"hash_tag":          func() error { return oneArg(d, &s.HashTag) },
		"co_locate_entries": func() error { return quietFlag(d, &s.CoLocateEntries) },
		"max_retention":     func() error { return durArg(d, &s.MaxRetention) },
		"max_clock_skew":    func() error { return durArg(d, &s.MaxClockSkew) },
		"no_clock_skew":     func() error { return quietFlag(d, &s.NoClockSkew) },
		"max_hard_epochs":   func() error { return intArg(d, &s.MaxHardEpochs) },
		"call_timeout":      func() error { return durArg(d, &s.CallTimeout) },
		"hard_epoch_wait":   func() error { return durArg(d, &s.HardEpochWait) },
		"skip_policy_check": func() error { return quietFlag(d, &s.SkipPolicyCheck) },
	})
	if err != nil {
		return err
	}
	h.Store = s
	return nil
}

// experimentsBlock parses `experiments { ... }` (docs/10 §3). Signing keys
// are kept as written, so {env.VAR} placeholders reach the JSON and are
// resolved at Provision; parse errors in this block never quote a token, in
// case it is a key.
func experimentsBlock(d *caddyfile.Dispenser, h *Handler) error {
	x := &ExperimentsConfig{}
	err := subBlock(d, "experiments", map[string]func() error{
		"cookie":     func() error { return secretArg(d, &x.Cookie) },
		"header":     func() error { return secretArg(d, &x.Header) },
		"max_active": func() error { return intArg(d, &x.MaxActive) },
		"signing_keys": func() error {
			args := d.RemainingArgs()
			if len(args) == 0 {
				return d.Errf("signing_keys takes at least one key")
			}
			x.SigningKeys = args
			return nil
		},
		"downstream_keys_on_variant": func() error { return optBool(d, &x.DownstreamKeysOnVariant) },
		"consent": func() error {
			args := d.RemainingArgs()
			if len(args) != 3 || args[0] != "cookie" {
				return d.Errf("consent takes: cookie <name> <value>")
			}
			x.Consent = &ConsentConfig{Cookie: args[1], Value: args[2]}
			return nil
		},
		"experiment": func() error { return experimentBlock(d, x) },
	}, "experiment")
	if err != nil {
		return err
	}
	h.Experiments = x
	return nil
}

func experimentBlock(d *caddyfile.Dispenser, x *ExperimentsConfig) error {
	var e ExperimentConfig
	if err := secretArg(d, &e.Name); err != nil {
		return err
	}
	// oneArg consumed the name and the opening brace is next; subBlock
	// expects no further argument.
	err := subBlock(d, "experiment", map[string]func() error{
		"match": func() error {
			args := d.RemainingArgs()
			if len(args) == 0 || len(args)%2 != 0 {
				return d.Errf("match takes host <host> and/or path <path>")
			}
			m := &ExperimentMatch{}
			for i := 0; i < len(args); i += 2 {
				switch args[i] {
				case "host":
					if m.Host != "" {
						return d.Errf("match host is set twice")
					}
					m.Host = args[i+1]
				case "path":
					if m.Path != "" {
						return d.Errf("match path is set twice")
					}
					m.Path = args[i+1]
				default:
					return d.Errf("match takes host and path only")
				}
			}
			e.Match = m
			return nil
		},
		"salt":   func() error { return secretArg(d, &e.Salt) },
		"winner": func() error { return secretArg(d, &e.Winner) },
		"variants": func() error {
			args := d.RemainingArgs()
			if len(args) == 0 {
				return argErr(d)
			}
			for _, a := range args {
				name, w, ok := strings.Cut(a, "=")
				n, err := strconv.Atoi(w)
				if !ok || name == "" {
					return d.Errf("variants take name=weight")
				}
				if err != nil {
					return d.Errf("variant weight is not an integer")
				}
				e.Variants = append(e.Variants, VariantConfig{Name: name, Weight: n})
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	x.Experiments = append(x.Experiments, e)
	return nil
}

// subBlock parses the nested block of one key. A block key is set once, and
// so is each key inside it.
func subBlock(d *caddyfile.Dispenser, name string, keys map[string]func() error, repeat ...string) error {
	if d.NextArg() {
		return argErr(d)
	}
	seen := map[string]bool{}
	nest := d.Nesting()
	empty := true
	for d.NextBlock(nest) {
		empty = false
		k := d.Val()
		fn, ok := keys[k]
		if !ok {
			return d.Errf("unknown key %s in %s block", tok(k), name)
		}
		if seen[k] && !slices.Contains(repeat, k) {
			return d.Errf("%q is set twice in %s block", k, name)
		}
		seen[k] = true
		if err := fn(); err != nil {
			return err
		}
	}
	// A bare "key" or "key {}" is a typo or a forgotten brace; it would
	// otherwise parse as a silent no-op.
	if empty {
		return d.Errf("%s requires a block with at least one key", name)
	}
	return nil
}

func oneArg(d *caddyfile.Dispenser, dst *string) error {
	if !d.NextArg() {
		return argErr(d)
	}
	*dst = d.Val()
	if d.NextArg() {
		return argErr(d)
	}
	return nil
}

// argErr replaces Caddy's ArgErr, which always quotes the last token it saw
// (and so can print a password or signing key that sits on a mis-nested or
// wrapped line).
func argErr(d *caddyfile.Dispenser) error {
	return d.Errf("wrong argument count or unexpected line ending")
}

// tok renders a token for an error. Only a token shaped like a directive name
// (lower case, at most 24 bytes) is quoted, so a typo such as "max_activ" is
// still named; anything else may be a secret and becomes <value>. A signing
// key is at least 32 bytes, so a real one never takes the quoted form.
func tok(s string) string {
	if s == "" || len(s) > 24 || s[0] < 'a' || s[0] > 'z' {
		return "<value>"
	}
	for i := range len(s) {
		c := s[i]
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_'
		if !ok {
			return "<value>"
		}
	}
	return strconv.Quote(s)
}

// secretArg reads one value that may be a credential. Caddy's ArgErr quotes the
// last token it saw, which here could be part of a password split by a space,
// so the message is fixed.
func secretArg(d *caddyfile.Dispenser, dst *string) error {
	key := d.Val()
	if !d.NextArg() {
		return d.Errf("%s takes exactly one argument (quote values that contain spaces)", key)
	}
	*dst = d.Val()
	if d.NextArg() {
		return d.Errf("%s takes exactly one argument (quote values that contain spaces)", key)
	}
	return nil
}

// quietFlag is flag without quoting a stray token: in the store block that
// token may be a mis-nested secret.
func quietFlag(d *caddyfile.Dispenser, dst *bool) error {
	key := d.Val()
	if d.NextArg() {
		return d.Errf("%s takes no arguments", key)
	}
	*dst = true
	return nil
}

// optBool is a flag that also accepts true or false (docs/10 §3 writes
// `downstream_keys_on_variant false`).
func optBool(d *caddyfile.Dispenser, dst *bool) error {
	key := d.Val()
	if !d.NextArg() {
		*dst = true
		return nil
	}
	switch d.Val() {
	case "true":
		*dst = true
	case "false":
		*dst = false
	default:
		return d.Errf("%s takes true or false", key)
	}
	if d.NextArg() {
		return d.Errf("%s takes at most one argument", key)
	}
	return nil
}

func listArg(d *caddyfile.Dispenser, dst *[]string) error {
	args := d.RemainingArgs()
	if len(args) == 0 {
		return argErr(d)
	}
	*dst = args
	return nil
}

// flag is a key with no value: its presence turns the setting on.
func flag(d *caddyfile.Dispenser, dst *bool) error {
	if d.NextArg() {
		return argErr(d)
	}
	*dst = true
	return nil
}

// ponytail: only the sign is checked here; upper bounds are weir.New's job at Provision (08 §2).
func intArg(d *caddyfile.Dispenser, dst *int) error {
	key := d.Val()
	var s string
	if err := oneArg(d, &s); err != nil {
		return err
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return d.Errf("%s: %s is not a non-negative integer", key, tok(s))
	}
	*dst = n
	return nil
}

func durArg(d *caddyfile.Dispenser, dst *caddy.Duration) error {
	key := d.Val()
	var s string
	if err := oneArg(d, &s); err != nil {
		return err
	}
	v, err := caddy.ParseDuration(s)
	if err != nil {
		return d.Errf("%s: invalid duration %s", key, tok(s))
	}
	if v < 0 {
		return d.Errf("%s: duration must not be negative", key)
	}
	*dst = caddy.Duration(v)
	return nil
}

var _ caddyfile.Unmarshaler = (*Handler)(nil)
