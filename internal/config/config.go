package config

import (
	"fmt"

	"github.com/spf13/viper"
)

type Config struct {
	Server  ServerConfig  `mapstructure:"server"`
	Matrix  MatrixConfig  `mapstructure:"matrix"`
	Webhook WebhookConfig `mapstructure:"webhook"`
	Logging LoggingConfig `mapstructure:"logging"`
}

type ServerConfig struct {
	Port int `mapstructure:"port"`
}

type MatrixConfig struct {
	Homeserver       string `mapstructure:"homeserver"`
	UserID           string `mapstructure:"userid"`
	AccessToken      string `mapstructure:"accesstoken"`
	DeviceID         string `mapstructure:"deviceid"`
	RecoveryKey      string `mapstructure:"recoverykey"`
	PickleKey        string `mapstructure:"picklekey"`
	RoomID           string `mapstructure:"roomid"`
	EnableEncryption bool   `mapstructure:"enable_encryption"`
	SyncTimeout      int    `mapstructure:"sync_timeout"`
	SkipInitialSync  bool   `mapstructure:"skip_initial_sync"`
}

type WebhookConfig struct {
	Default          string            `mapstructure:"default"`
	Commands         map[string]string `mapstructure:"commands"`
	Template         string            `mapstructure:"template"`
	CommandTemplates map[string]string `mapstructure:"command_templates"`
	AuthTokens       map[string]string `mapstructure:"auth_tokens"`
	DefaultAuth      string            `mapstructure:"default_auth"`
	JQSelector       string            `mapstructure:"jq_selector"`
	CommandSelectors map[string]string `mapstructure:"command_selectors"`
	SkipEmpty        bool              `mapstructure:"skip_empty"`
	Timeout          int               `mapstructure:"timeout"`
}

type LoggingConfig struct {
	Level string `mapstructure:"level"`
	File  string `mapstructure:"file"`
}

func LoadConfig() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")
	viper.AddConfigPath("../config")

	// Set default values
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("webhook.template", `{"message": "{{MESSAGE}}"}`)
	viper.SetDefault("webhook.timeout", 30)
	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.file", "")
	viper.SetDefault("matrix.enable_encryption", true)
	viper.SetDefault("matrix.sync_timeout", 120)
	viper.SetDefault("matrix.skip_initial_sync", false)

	// Environment variable support
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &config, nil
}