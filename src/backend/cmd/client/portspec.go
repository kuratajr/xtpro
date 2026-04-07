package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type portSpec struct {
	protocol  string
	localPort int

	// remotePort <= 0 means auto-allocate (server assigns).
	remotePort int

	raw string
}

func parsePortSpecsFromString(input string) ([]portSpec, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, nil
	}
	tokens := strings.FieldsFunc(input, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	return parsePortSpecs(tokens)
}

func parsePortSpecs(args []string) ([]portSpec, error) {
	tokens := make([]string, 0, len(args))
	for _, a := range args {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		// Allow comma-separated specs inside a single arg.
		for _, t := range strings.FieldsFunc(a, func(r rune) bool { return r == ',' }) {
			t = strings.TrimSpace(t)
			if t != "" {
				tokens = append(tokens, t)
			}
		}
	}
	if len(tokens) == 0 {
		return nil, nil
	}

	out := make([]portSpec, 0, len(tokens))
	for _, tok := range tokens {
		spec, err := parseOnePortSpec(tok)
		if err != nil {
			return nil, err
		}
		out = append(out, spec)
	}
	return out, nil
}

func parseOnePortSpec(tok string) (portSpec, error) {
	raw := tok
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return portSpec{}, fmt.Errorf("port spec rỗng")
	}

	parts := strings.Split(tok, ":")
	switch len(parts) {
	case 1:
		local, err := parsePort(parts[0])
		if err != nil {
			return portSpec{}, fmt.Errorf("port spec %q không hợp lệ: %w", raw, err)
		}
		return portSpec{protocol: "tcp", localPort: local, remotePort: 0, raw: raw}, nil
	case 2:
		p0 := strings.ToLower(strings.TrimSpace(parts[0]))
		if isSupportedProto(p0) {
			local, err := parsePort(parts[1])
			if err != nil {
				return portSpec{}, fmt.Errorf("port spec %q không hợp lệ: %w", raw, err)
			}
			return portSpec{protocol: p0, localPort: local, remotePort: 0, raw: raw}, nil
		}
		local, err := parsePort(parts[0])
		if err != nil {
			return portSpec{}, fmt.Errorf("port spec %q không hợp lệ: %w", raw, err)
		}
		remote, err := parsePort(parts[1])
		if err != nil {
			return portSpec{}, fmt.Errorf("port spec %q không hợp lệ: %w", raw, err)
		}
		return portSpec{protocol: "tcp", localPort: local, remotePort: remote, raw: raw}, nil
	case 3:
		proto := strings.ToLower(strings.TrimSpace(parts[0]))
		if !isSupportedProto(proto) {
			return portSpec{}, fmt.Errorf("port spec %q không hợp lệ: protocol %q không hỗ trợ", raw, parts[0])
		}
		local, err := parsePort(parts[1])
		if err != nil {
			return portSpec{}, fmt.Errorf("port spec %q không hợp lệ: %w", raw, err)
		}
		remote, err := parsePort(parts[2])
		if err != nil {
			return portSpec{}, fmt.Errorf("port spec %q không hợp lệ: %w", raw, err)
		}
		return portSpec{protocol: proto, localPort: local, remotePort: remote, raw: raw}, nil
	default:
		return portSpec{}, fmt.Errorf("port spec %q không hợp lệ: sai định dạng (vd: tcp:2222:11111 hoặc 2222:11111)", raw)
	}
}

func isSupportedProto(p string) bool {
	return p == "tcp" || p == "udp" || p == "http"
}

func parsePort(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("thiếu port")
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 || n > 65535 {
		return 0, fmt.Errorf("port không hợp lệ: %q", s)
	}
	return n, nil
}
