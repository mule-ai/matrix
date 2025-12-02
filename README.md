# Matrix Microservice

A lightweight microservice that integrates with Matrix chat and forwards messages to webhooks.

## Features

- Statically compiled Go binary
- YAML configuration
- Single POST endpoint for message submission
- Webhook support with `{{MESSAGE}}` template
- Slash command routing to different webhooks
- Bearer token authorization for webhooks
- Comprehensive logging for monitoring
- Status endpoints for health and configuration checking
- End-to-end encryption support
- Single user/channel support
- Bidirectional communication (send and receive Matrix messages)

## Configuration

Create a `config.yaml` file:

```yaml
server:
  port: 8080

matrix:
  homeserver: "https://matrix.example.com"
  userid: "@bot:example.com"
  accesstoken: "your_access_token_here"
  deviceid: "device_id"
  recoverykey: "your_recovery_key_here"
  picklekey: "your_pickle_key_here"
  roomid: "!roomid:example.com"
  enable_encryption: true

webhook:
  default: "http://localhost:3000/webhook"
  template: |
    {
      "text": "{{MESSAGE}}"
    }
  auth_tokens:
    default: "Bearer your-default-token-here"
    alert: "Bearer your-alert-token-here"
    status: "Bearer your-status-token-here"
  default_auth: "default"
  commands:
    alert: "http://localhost:3000/alert"
    status: "http://localhost:3000/status"

logging:
  level: "info"
  file: ""
```

### Authorization Configuration

- `auth_tokens`: Map of token names to Bearer tokens
- `default_auth`: Name of the default token to use for all webhooks (unless overridden)
- Each command can have its own token in the `auth_tokens` map
- If a command doesn't have a specific token, it will fall back to the default token

### Logging Configuration

- `level`: Log level (debug, info, warn, error)
- `file`: Optional file path to write logs to (in addition to stdout)

### Encryption Configuration

- `recoverykey`: Your Matrix account's recovery key for encryption
- `picklekey`: Secret key used to encrypt the crypto database (use a strong random key)
- `enable_encryption`: Whether to enable end-to-end encryption (default: true)

## Usage

### Build and Run

```bash
# Build the binary
make build

# Run the service
make run

# Or run directly
./matrix-microservice
```

### Docker

```bash
# Build Docker image
make docker-build

# Run Docker container
make docker-run
```

### API Endpoints

1. `POST /message` - Send a message to Matrix and forward to webhook
   ```json
   {
     "message": "Hello, world!",
     "as_file": true,
     "filename": "report.md"
   }
   ```

   Parameters:
   - `message` (required): The message content to send
   - `as_file` (optional): Boolean flag to send the message as a file attachment. Defaults to `false`.
   - `filename` (optional): Filename for the attachment when `as_file` is `true`. Defaults to `message.md`.

2. `GET /health` - Health check endpoint
3. `GET /status` - Detailed status including Matrix and webhook configuration

### Slash Commands

Messages that start with `/` followed by a command name will be routed to specific webhooks:

- `/alert Something happened` - Routes to the "alert" webhook
- `/status check` - Routes to the "status" webhook
- `/unknown command` - Routes to the default webhook

### Bidirectional Communication

The service supports bidirectional communication with Matrix:

1. **Outgoing Messages**: Send messages to Matrix via the `POST /message` endpoint
2. **Incoming Messages**: Receive messages from Matrix when the bot is mentioned

When the bot receives a message in Matrix:
1. It filters messages to only process those directed at the bot (mentions)
2. It removes the bot mention from the message text
3. It extracts any slash commands from the message
4. It dispatches the message to the appropriate webhook based on the command

For example, if a user sends "@mule /alert Something happened" in Matrix:
1. The service detects the mention of "@mule"
2. It removes "@mule" from the message, leaving "/alert Something happened"
3. It extracts the "alert" command
4. It dispatches the message to the "alert" webhook configured in the YAML file

## Monitoring

The service provides comprehensive logging to help monitor its operation:

- Startup and shutdown events
- Matrix connection status
- Encryption initialization
- Message receipt and processing
- Webhook dispatch and responses
- Error conditions

Additionally, the `/status` endpoint provides runtime configuration information.

## Dependencies

- Go 1.24+
- Matrix account with access token
- Webhook endpoints to receive messages