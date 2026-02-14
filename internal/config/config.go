package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"

	"github.com/spf13/viper"
)

func GeneratePickleKey() string {
	// Generate a 32-byte random key for encryption
	bytes := make([]byte, 32)
	_, err := rand.Read(bytes)
	if err != nil {
		panic(fmt.Sprintf("failed to generate random key: %v", err))
	}
	return base64.StdEncoding.EncodeToString(bytes)
}

func SaveConfig(config *Config) error {
	// Write the current config back to the file
	viper.Set("matrix.picklekey", config.Matrix.PickleKey)
	viper.Set("matrix.accesstoken", config.Matrix.AccessToken)
	viper.Set("matrix.deviceid", config.Matrix.DeviceID)

	// Try to find the config file path
	configFile := "config.yaml"
	if _, err := os.Stat("./config.yaml"); err == nil {
		configFile = "./config.yaml"
	} else if _, err := os.Stat("../config.yaml"); err == nil {
		configFile = "../config.yaml"
	}

	return viper.WriteConfigAs(configFile)
}

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
	// Command execution settings
	EnableCommands bool   `mapstructure:"enable_commands"`
	CommandPrefix  string `mapstructure:"command_prefix"`
	SessionTimeout int    `mapstructure:"session_timeout"`
	// Default command to execute (e.g., "pi -p")
	DefaultCommand string `mapstructure:"default_command"`
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
	// Command execution defaults
	viper.SetDefault("webhook.enable_commands", false)
	viper.SetDefault("webhook.command_prefix", "/cmd")
	viper.SetDefault("webhook.session_timeout", 600) // 10 minutes
	viper.SetDefault("webhook.default_command", "")

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

// EnsurePickleKey generates a pickle key if one is not set in the config
func (c *Config) EnsurePickleKey() bool {
	if c.Matrix.PickleKey == "" || c.Matrix.PickleKey == "your_pickle_key_here" {
		c.Matrix.PickleKey = GeneratePickleKey()
		return true
	}
	return false
}
