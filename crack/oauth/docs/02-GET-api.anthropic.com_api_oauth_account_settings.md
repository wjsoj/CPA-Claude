# 02. GET https://api.anthropic.com/api/oauth/account/settings

**阶段**：启动期 bootstrap **状态码**：200 **请求大小**：0 B **响应大小**：920 B

**用途**：拉取 claude.ai 账号偏好（onboarding 状态、横幅 dismiss、特性开关）。

## 请求行

```
GET https://api.anthropic.com/api/oauth/account/settings
```

## 请求头（共 7 个）

| Header | Value |
|---|---|
| accept | application/json, text/plain, */* |
| accept-encoding | gzip, br |
| authorization | Bearer sk-ant-oat01-REDACTED |
| user-agent | claude-code/2.1.126 |
| anthropic-beta | oauth-2025-04-20 |
| host | api.anthropic.com |
| connection | close |

## 请求体

_无_

## 响应头（共 17 个）

| Header | Value |
|---|---|
| date | Sun, 03 May 2026 15:27:56 GMT |
| content-type | application/json |
| transfer-encoding | chunked |
| connection | close |
| request-id | req_REDACTED |
| strict-transport-security | max-age=31536000; includeSubDomains; preload |
| anthropic-organization-id | 00000000-0000-0000-0000-000000000002 |
| server | cloudflare |
| x-envoy-upstream-service-time | 132 |
| content-encoding | gzip |
| vary | Accept-Encoding |
| server-timing | x-originResponse;dur=134 |
| cf-cache-status | DYNAMIC |
| set-cookie | _cfuvid=REDACTED; HttpOnly; SameSite=None; Secure; Path=/; Domain=api.anthropic.com |
| content-security-policy | default-src 'none'; frame-ancestors 'none' |
| x-robots-tag | none |
| cf-ray | REDACTED-cf-ray |

## 响应体

- **Content-Type**：`application/json`
- **解码后大小**：`2504` B
- **格式**：JSON (object)

### 顶层字段
| Field | Type | Sample |
|---|---|---|
| input_menu_pinned_items | null | null |
| has_seen_mm_examples | null | null |
| has_seen_starter_prompts | null | null |
| has_started_claudeai_onboarding | bool | true |
| has_finished_claudeai_onboarding | bool | true |
| dismissed_claudeai_banners | array[1] | [object{2}, ...] |
| dismissed_artifacts_announcement | null | null |
| preview_feature_uses_artifacts | null | null |
| preview_feature_uses_latex | null | null |
| preview_feature_uses_citations | null | null |
| preview_feature_uses_harmony | null | null |
| enabled_artifacts_attachments | bool | false |
| enabled_turmeric | null | null |
| enable_chat_suggestions | null | null |
| dismissed_artifact_feedback_form | null | null |
| enabled_mm_pdfs | null | null |
| enabled_gdrive | null | null |
| enabled_bananagrams | null | null |
| enabled_gdrive_indexing | null | null |
| enabled_web_search | bool | true |
| enabled_compass | null | null |
| enabled_sourdough | null | null |
| enabled_foccacia | null | null |
| enabled_yukon_gold | null | null |
| dismissed_claude_code_spotlight | null | null |
| enabled_geolocation | null | null |
| enabled_mcp_tools | null | null |
| enabled_connector_suggestions | null | null |
| enabled_cli_ops | null | null |
| enabled_megaminds | null | null |
| paprika_mode | string | "off" |
| default_model | null | null |
| enabled_full_thinking | null | null |
| tool_search_mode | string | "auto" |
| enabled_monkeys_in_a_barrel | null | null |
| enabled_wiggle_egress | null | null |
| wiggle_egress_allowed_hosts | null | null |
| wiggle_egress_hosts_template | null | null |
| wiggle_egress_spotlight_viewed_at | null | null |
| browser_extension_settings | null | null |
| enabled_saffron | null | null |
| enabled_saffron_search | null | null |
| enabled_melange | null | null |
| internal_melange_store_id | null | null |
| orbit_enabled | null | null |
| orbit_timezone | null | null |
| dismissed_saffron_themes | bool | true |
| grove_enabled | bool | true |
| grove_updated_at | string | "2026-04-20T15:11:13.706857Z" |
| grove_notice_viewed_at | null | null |
| internal_tier_org_type | null | null |
| internal_tier_rate_limit_tier | null | null |
| internal_tier_seat_tier | null | null |
| internal_tier_override_expires_at | null | null |
| has_acknowledged_mcp_app_dev_terms | null | null |
| onboarding_use_case | null | null |
| voice_preference | null | null |
| voice_speed | null | null |
| voice_language_code | null | null |
| ccr_sharing_enforce_repo_check | null | null |
| ccr_sharing_show_display_name | null | null |
| ccr_sharing_auto_share_on_pr | null | null |
| ccr_auto_archive_on_pr_close | null | null |
| ccr_autofix_on_pr_create | null | null |
| ccr_auto_create_pr_on_push | null | null |
| ccr_auto_create_pr_as_draft | null | null |
| ccr_session_state_buckets | null | null |
| ccr_persistent_memory | null | null |
| ccr_plugins_mount | null | null |
| cowork_sms_enabled | null | null |
| cowork_onboarding_completed_at | null | null |
| dittos_mobile_onboarding_seen_at | null | null |
| internal_cowork_trial_started_at | null | null |
| internal_cowork_trial_ends_at | null | null |
| internal_has_used_remote_control | null | null |
| internal_tangelo_credit_claimed | null | null |
| internal_cc_onboarding_settings | null | null |

---
_原始 JSON_：[`rows/02-GET-api.anthropic.com_api_oauth_account_settings.json`](../rows/02-GET-api.anthropic.com_api_oauth_account_settings.json)
