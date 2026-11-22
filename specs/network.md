# Game gateway: UDP, KCP, frames, encryption, sessions

Wire protocol, KCP transport settings, frame layout, session encryption, login, and reconnect for
the 3.8 client.

## 1. Socket

- One UDP socket, default `127.0.0.1:13100` (the login server hands this address to the
  client, `login.md`). All sessions share it.
- Datagrams up to 1500 bytes are read. A datagram is one of:
  - a **connect** datagram: exactly 35 bytes, first byte `0xED`;
  - a **KCP** datagram: at least 24 bytes (the KCP header); its first 4 bytes are the
    conversation id, little-endian.
  - Anything else is dropped.

## 2. Connect handshake

1. The client sends the 35-byte connect datagram. Only the first byte is checked; the other
   34 bytes are not read (their meaning is unknown; capture them in R-143d and record it here).
2. The server picks a new conversation id: a counter starting at 1, skipping 0 and ids already
   issued since the server started. Ids are never reused while the server runs.
3. It answers with 5 bytes: `0xEE`, then the conversation id as u32 little-endian, sent to the
   datagram's source address.
4. From then on that conversation id belongs to that source address. KCP datagrams carrying
   the id from another address are ignored.

Each connect datagram starts a new session, even from the same address.

## 3. KCP

- Standard KCP (`ikcp` algorithm). Conversation id from §2.
- Server settings: the ikcp defaults. MTU 1400, send window 32, receive window 256,
  no-delay off, interval 100 ms, fast resend off, congestion control on, dead link after
  20 retransmissions, minimum RTO 100 ms, message mode (not stream).
- Client settings: MTU 1000 unless the server announced one (`RemoteMtu`),
  windows 256/256, `NoDelay(1, 10, 2, 1)`, **stream mode**. So the bytes the server receives
  form a stream: one frame (§4) may span several KCP messages and one KCP message may hold
  several frames or parts of them. The server must reassemble frames across KCP messages.
- Each outgoing frame goes into KCP as one `send` of the whole frame, length prefix included.
- After feeding a datagram to KCP, the server flushes at once; KCP's own update runs at the
  time KCP asks for (`check`), measured from the session's start.
- A session closes when: KCP reports a dead link; three sends in a row fail; no datagram
  arrived for 10 s before login finished (§6) or for 5 minutes after; the server shuts down;
  or the same player logs in from another session (§7).
- Fairness: inbound, outbound and timer work for all sessions run in turns, at most 64
  steps or 2 ms per turn before the loop looks for new datagrams again.

## 4. Frames

Every application message, both directions, is one frame. Integers are little-endian.

| Bytes | Field |
|---|---|
| 3 | length of everything after these 3 bytes (u24) |
| 1 | message type: 1 request, 2 response, 3 exception, 4 push |
| 4 | sequence number (u32) |
| 2 | rpc id (u16): **request and response only** |
| 2 | message id (u16) |
| 4 | CRC-32 (IEEE, as zlib) of the payload bytes as they are in the frame (after encryption) |
| n | payload: the protobuf message, encrypted once a session key exists (§5) |

- Header sizes: request and response 13 bytes after the length, push 11.
- The client never needs type 3 from us: the server sends only 2 and 4. The client reads a
  type 3 as `(seqNo, rpcId, errorCode, string)`; its layout is unknown and not needed.
- A frame whose CRC does not match closes the session.
- Message ids are the 3.8 protocol message ids generated into `server/protocol/msgid.go`.

**Sequence numbers.** The server keeps one counter per session, starting at 0; every frame it
sends (response or push) takes the counter's next value, so the first frame is 1. Requests
carry the client's own upstream counter (`UpStreamSeqNo`) and are not checked. The client
remembers the last server sequence number it saw (`DownStreamSeqNo`) and sends it in
`ReconnectRequest.LastSvrSeqNo`.

**Rpc ids.** A response carries the rpc id of the request it answers. The response's message
id is the one paired with the request (`XRequest` → `XResponse`).

## 5. Encryption

### 5.1 The session key

- The first request is `ProtoKeyRequest` (message id 111), unencrypted.
- The server makes 32 random bytes: the session key.
- It encrypts them with RSA PKCS#1 v1.5 (type 2 padding, random non-zero padding bytes)
  under the public key in `state/gateway/client_public_key.der` (a DER `RSAPublicKey`:
  SEQUENCE of modulus and exponent; 2048-bit, so the result is 256 bytes). The client holds
  the matching private key.
- It answers `ProtoKeyResponse { Type = 2, Key = <256 bytes> }`. This response is **not**
  encrypted. `Type` 2 enables AES encryption (0 would be no encryption).
- Every frame after that response, in both directions, is encrypted with the session key.
- A new `ProtoKeyRequest` (the client sends one before each reconnect) makes a new key, with
  the same rule: its response unencrypted, everything after it under the new key.
- **Without a key file** (R-173b): the server answers `ProtoKeyResponse { Type = 0 }` (no key)
  and the session stays in the clear both ways. The stock client still encrypts after Type 0:
  `Net.SetDynamicProtoKey` only passes the key to the native `SetK`, and the cipher is chosen
  per message id (`KcpClient.SetEnType(type, id)`; `Net.Initialize` sets Rsa = 2 for 111/112,
  every other id keeps the native default Aes = 0; client enum `Aes 0, NoEncrypt 1, Rsa 2`).
  Our C# patch (type `RB.RBNet` in tools/rbauto, hooked into `Net.SetDynamicProtoKey` by cspatch) sets
  every other id to NoEncrypt on Type 0, and back to Aes on a later Type 2.

### 5.2 Payload cipher

AES-256 in ECB mode with the session key, PKCS#7 padding, plus one XOR on one byte:

Encrypt (server → client):

1. `p` = the protobuf bytes, `n` = their length.
2. If `n` is 0, the payload is empty: send no bytes.
3. Else XOR `p[seq mod n]` with `key[seq mod 32]`, where `seq` is the frame's sequence number.
4. Pad with PKCS#7 to a multiple of 16 (a full block of `0x10` when `n` is already a multiple
   of 16) and encrypt each block with AES-256-ECB.

Decrypt (client → server):

1. If the payload is empty, the message is empty.
2. Decrypt each 16-byte block with AES-256-ECB, drop the PKCS#7 padding (the last byte says how
   many bytes).
3. If the result is not empty, XOR byte `seq mod length` with `key[seq mod 32]`, using the
   request's own sequence number.

Once a session key exists, the server decrypts every incoming frame.

## 6. Login sequence on the gateway

Until a session has entered the game, only these requests are accepted; anything else, or a
push, closes the session:

| Request | Answer and effect |
|---|---|
| `ProtoKeyRequest` | §5.1 |
| `LoginRequest` | Look up or create the player (below). Answer `LoginResponse { ReconnectToken, Timestamp }`; other fields default (Code 0 = success). The session is not in the game yet. |
| `EnterGameRequest` | Answer `EnterGameResponse {}` (empty). The session enters the game: the player's state loads and the enter-game sequence runs (pushes that build the client's world: `enter-world.md`). |
| `ReconnectRequest` | §8 |
| `HeartbeatRequest` | Answer `HeartbeatResponse {}` (accepted while entering as well as in game). |

**Player lookup at `LoginRequest`.** `LoginRequest.Account` is the account name (the login
server's `userId`, `login.md`).

1. Make a file-safe name: replace each of `< > : " / \ | ? *` and every byte 0–31 with `_`.
2. If `state/account/link/<safe name>` exists, it holds `.{ .player_id = N }`: the player is N.
3. Otherwise take the next player id from the counter file `state/player/next` (one decimal
   number; when the file is missing, the first id is 666 and the file is created holding the
   next one), and write the link file.
4. `ReconnectToken`: 32 random bytes as 64 lower-case hex digits, written to
   `state/player/<id>/reconnect_token` (replacing the old one).
5. `Timestamp`: the current real time in milliseconds since the Unix epoch.

The `Token` and every other `LoginRequest` field are not checked. (The token is checked once,
by the login server.)

**At `EnterGameRequest`** the server also starts a fresh session for the game logic: the quest
offers and flow queue start empty, and the client is known to hold no entities from an
earlier run (R-117, R-89, R-121; these belong to the specs of those systems).

## 7. One session per player

When a player enters (or reconnects) while another session holds the same player id, the old
session is closed (reason "replaced") and the new one waits until the old one has released the
player (saved and closed) before its own state loads. A player's state is never loaded twice.

## 8. Reconnect

`ReconnectRequest` arrives on a new session (new connect handshake, new `ProtoKeyRequest`).

1. If `PlayerId` ≤ 0, or `state/player/<PlayerId>/basic_info` does not exist, or the token is
   not 64 characters, or it differs from `state/player/<PlayerId>/reconnect_token`: answer
   `ReconnectResponse { ErrorCode = ErrReconnectGwgetGatePlayerFailed, Timestamp,
   IsPermittedSilentLogin = true }` and stay out of the game (the client then logs in again).
2. Otherwise the session enters the game as player `PlayerId` (§7 applies), then answers
   `ReconnectResponse { ErrorCode = Success, LastRecvSeqNo = <the request's LastSvrSeqNo>,
   Timestamp }`, then loads the state and runs the enter-game sequence, as after
   `EnterGameRequest`.
   - Echoes the client's `LastSvrSeqNo` back as `LastRecvSeqNo`; the client then resends its
     cached requests after that sequence number.
   - Differences from a login: the client keeps its entities, so the first world refresh
     removes the old run's ones; offers go out again.

## 9. After entering

- Requests go to their handlers. A request with no handler gets an empty
  response with the paired response id and the request's rpc id, so the client never waits;
  the log line is `no handler for <MessageName> (msg_id: N)` (plus the payload in hex when it is
  at most 512 bytes). Keep the log line, because the audit tools read it.
- A push from the client with no handler is logged the same way and dropped.
- A frame that fails to decode closes the session.

## 10. Log lines other tools read

The start scripts and `tools/tl_audit.py` read the server logs. Keep these texts:

- `game server is listening at <addr>` style startup lines: the start scripts wait for
  "is listening at".
- `no handler for <Name> (msg_id: <id>)` (`tools/tl_audit.py`).

## 11. Tests to write (R-143d acceptance)

- KCP: round trip of messages larger than one MTU; loss and reordering; a stream-mode sender.
- Frames: encode/decode both header kinds; CRC failure; a frame split across KCP messages and
  several frames in one.
- Cipher: lengths 0, 1, 15, 16, 17, 33; the XOR byte for `seq` values around multiples of `n`.
- Key exchange: the RSA output decrypts with a test key pair; `ProtoKeyResponse` goes out
  unencrypted and the next frame encrypted (sequence numbers 1 then 2).
- Login: link file created and reused; a name with `:`; reconnect with a wrong token.
- Real client: connects and reaches the login flow.
