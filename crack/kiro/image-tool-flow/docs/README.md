# Image-tool flow — Kiro CLI capture (2026-06-02)

Captures from running real `kiro` CLI against `q.us-east-1.amazonaws.com`
while asking it to read an image file. Used to validate hypitoken's
Anthropic→Kiro translation: the previous translator silently dropped
image blocks from `tool_result.content`, so Claude Code-driven image reads
returned a 400 ValidationException with `IMAGE_FORMAT_UNSUPPORTED`.

## The flow

| # | Stage | Status | Model | Notes |
|---|---|---|---|---|
| 01 | Turn 1 — ask to read dir | 200 | minimax-m2.5 | model emits `read` tool_use, mode=`Directory` |
| 02 | Turn 2 — ask to read image | 200 | minimax-m2.5 | model emits `read` tool_use, mode=`Image`, `image_paths` |
| 03 | Turn 3 — image bytes uploaded | **400** | minimax-m2.5 | Bedrock: "this model doesn't support image content block" |
| 04 | Retry with glm-5 — tool_use | 200 | glm-5 | model re-emits same `read` tool_use |
| 05 | Retry with glm-5 — image upload | **400** | glm-5 | same Bedrock error |
| 06 | New conversation, Claude Haiku | 200 | claude-haiku-4.5 | model emits read tool_use |
| 07 | Claude Haiku follow-up | 200 | claude-haiku-4.5 | another tool_use round |
| 08 | **Claude Haiku image upload** | **200** | claude-haiku-4.5 | gold standard — image accepted |

## Image-bearing wire shape (from row 08, the SUCCESS case)

```jsonc
{
  "conversationState": {
    "currentMessage": {
      "userInputMessage": {
        "content": "",
        "userInputMessageContext": {
          "envState": { ... },
          "tools": [ ... ],
          "toolResults": [
            {
              "toolUseId": "tooluse_…",
              "status": "success",
              "content": []                       // ← intentionally empty
            }
          ]
        },
        "origin": "KIRO_CLI",
        "modelId": "claude-haiku-4.5",
        "images": [                               // ← TOP-LEVEL of userInputMessage
          { "format": "png", "source": { "bytes": "<base64>" } }
        ]
      }
    },
    …
  },
  "profileArn": "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK"
}
```

**Key takeaway:** images do NOT live inside `tool_result.content`. They live
at `userInputMessage.images[]`. `tool_result.content` is left as an empty
array with `status:"success"` — Kiro/Bedrock pairs the image visually with
the tool_result by ordering.

## The bug we fixed (in cc-core kirobridge)

`convertToolResult.toolResultContent` only retained `text` blocks from the
Anthropic-side `tool_result.content`. When Claude Code returned an image
via `[{type:"image", source:{type:"base64", media_type:"image/png", data:"…"}}]`,
the image was silently dropped and the user-side `userInputMessage.images`
stayed empty — the model knew there was a tool_result but never saw any image.

Fix: `convertToolResult` now returns extracted image attachments alongside
the result; `splitUserContent` and `buildHistory` accumulate them into the
top-level `Images` slice of the enclosing user message, matching the wire
shape above.
