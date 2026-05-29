// Package config exposes small helpers for reading typed values from
// environment variables. Services must call [MustString] for required
// secrets (so misconfiguration fails the pod at startup, before /readyz
// goes green).
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// String returns the value of name, or def if unset.
func String(name, def string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return def
}

// MustString panics if name is unset or empty.
func MustString(name string) string {
	v := os.Getenv(name)
	if v == "" {
		panic(fmt.Sprintf("required env var %s is not set", name))
	}
	return v
}

// Int parses name as a base-10 int, or returns def.
func Int(name string, def int) int {
	v, ok := os.LookupEnv(name)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		panic(fmt.Sprintf("env %s: not an int: %v", name, err))
	}
	return n
}

// Duration parses name with [time.ParseDuration].
func Duration(name string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(name)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		panic(fmt.Sprintf("env %s: not a duration: %v", name, err))
	}
	return d
}

// Float parses name as a float64, or returns def.
func Float(name string, def float64) float64 {
	v, ok := os.LookupEnv(name)
	if !ok {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		panic(fmt.Sprintf("env %s: not a float: %v", name, err))
	}
	return f
}

// Bool parses name as a bool. Anything other than "1", "true", "yes" is false.
func Bool(name string, def bool) bool {
	v, ok := os.LookupEnv(name)
	if !ok {
		return def
	}
	switch v {
	case "1", "true", "TRUE", "yes":
		return true
	}
	return false
}
