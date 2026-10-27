package config

import (
	"fmt"
	"net"
	"os"
	"time"
)

type Config struct {
	HTTPAddr        string
	DatabaseURL     string
	ShutdownTimeout time.Duration
}

func Load() (Config, error) {
	c := Config{HTTPAddr: os.Getenv("HTTP_ADDR"), DatabaseURL: os.Getenv("DATABASE_URL"), ShutdownTimeout: 10 * time.Second}
	if c.HTTPAddr == "" {
		c.HTTPAddr = ":8088"
	}
	if _, _, err := net.SplitHostPort(c.HTTPAddr); err != nil {
		return Config{}, fmt.Errorf("HTTP_ADDR: %w", err)
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	return c, nil
}
