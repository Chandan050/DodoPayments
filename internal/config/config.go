package config

import "os"

type Config struct {
	Port        string
	DatabaseURL string
	PSPURL      string
	APIKey      string
}

func Load() Config {
	return Config{
		Port:        getEnv("PORT", "8080"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://dodo:dodo@localhost:5432/dodo?sslmode=disable"),
		PSPURL:      getEnv("MOCK_PSP_URL", "http://localhost:8081"),
		APIKey:      getEnv("DEMO_API_KEY", "dev_business_key_0123456789"),
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
