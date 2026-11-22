# Login server (HTTP)

HTTP server handling account login, ping, and health checks for the 3.8 client.

## Listening

- Plain HTTP/1.1 on TCP, default `127.0.0.1:10001` (option `--listen_address`).
- Options: `--state_dir` (default `state`), `--gateway_address` (default `127.0.0.1:13100`,
  the game gateway handed to the client).
- Startup log line: `login server is listening at <addr>` (the start scripts wait for it).
- One request per connection: read the request line, answer, close. Only the request line is
  read (`METHOD SP PATH[?QUERY] SP VERSION CR LF`); headers and body are ignored.
- Every response: `HTTP/1.1 <code> <reason>`, then `Content-Length` and `Content-Type` when
  there is a body, a blank line, the body. No other headers.
- A method other than `GET`: 405, no body. An unknown path: 404, no body, log
  `unhandled request path: <path>`.

## Routes

| Path | Answer |
|---|---|
| `/health` | 200, no body |
| `/api/ping` | 200, `application/json`, body `{"clientIp":"<gateway host>"}` (the 3.8 client JSON-decodes it for UDP delay probing; an empty 404 logs a JSON error at each login) |
| `/api/login` | the login below |
| `/api/CopyUserLogin` | the same login (the 3.8 login view's default "copy account" mode posts here; nothing is copied) |

Paths the 3.8 client asks for that receive 404:
`/TDCfg/config` (44 times per session) and `/force_update/OptionalUpdate.json`. A 404 works
today.

## Login

Query string: `name=value` pairs joined by `&`, no percent-decoding (values are used as
they come). A pair without `=`, with two `=`, with an unknown name, or with a number that does
not parse is a bad request: 400, no body.

| Name | Type | Required |
|---|---|---|
| `loginType` | 0 or 1 | yes |
| `userId` | text | yes |
| `userName` | text | yes |
| `token` | text | yes |
| `userData` | i32 | yes |
| `deviceId` | text | no |
| `loginTraceId` | text | no |
| `sourceAccount` | text | no (CopyUserLogin; read, not used) |
| `copyUserLoginMode` | i32 | no (CopyUserLogin; read, not used) |

A missing required one is a bad request too.

Account file `state/account/<userId>` (ZON, `save-format.md` §3):

- If it exists and its `token` differs from the request's: answer
  `{"code":1,"hasRpc":true,"errMessage":"Login Failed","userData":<userData>}`.
- If it does not exist: create `.{ .user_id = <userId>, .user_name = <userName>,
  .token = <token>, .sex = 0 }`.
- If it cannot be read or parsed: `{"code":-1,"hasRpc":true,"errMessage":"Internal Server
  Error","userData":<userData>}`.

Success, 200 `application/json`, fields in this order, no null fields:

```json
{"code":0,"hasRpc":true,"token":"<account token>","udpHosts":[{"host":"<gateway host>","port":<gateway port>}],"userData":<userData>,"sex":<account sex>}
```

`sex` is written into the account file by the game server when the player picks a gender
(`save-format.md`, account files).

The client then connects to the gateway and sends `LoginRequest.Account = userId`
(`network.md` §6).
