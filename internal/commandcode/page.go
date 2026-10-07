package commandcode

import (
	"fmt"
	"html"
	"strings"
)

// accountPageCopy holds the bilingual strings for the account page. Adding a
// language means adding one map entry; the page itself never hardcodes text.
var accountPageCopy = map[string]map[string]string{
	"zh": {
		"title":            "CommandCode 账号",
		"subtitle":         "账号与 CPA 凭据同一份数据：这里做的改动，CPAMP 凭证管理同样可见。",
		"add":              "新增账号",
		"api_key":          "CommandCode API Key（user_ 开头）",
		"label":            "显示名（可选）",
		"base_url":         "上游地址（可选，默认官方）",
		"save":             "保存",
		"save_hint":        "同一 Key 重复保存不会产生第二条账号。",
		"list":             "账号列表",
		"refresh":          "刷新",
		"empty":            "还没有账号。请在上方新增，或在 CPAMP 凭证管理中导入。",
		"loading":          "加载中…",
		"status":           "状态",
		"cooldown":         "冷却至",
		"enable":           "启用",
		"disable":          "停用",
		"edit":             "编辑",
		"remove":           "移出",
		"restore":          "恢复",
		"purge":            "彻底删除",
		"removed":          "已移出（可恢复）",
		"write_off":        "插件页写入已关闭（allow-page-write=false），当前为只读。",
		"key_required":     "请输入 API Key。",
		"key_prefix":       "API Key 必须以 user_ 开头。",
		"base_url_invalid": "上游地址必须以 http:// 或 https:// 开头。",
		"file_required":    "缺少文件参数。",
		"dir_unknown":      "无法定位凭据目录（请先导入一个账号或在 CPAMP 中导入）。",
		"write_disabled":   "插件页写入已关闭。",
		"remove_note":      "已移出，宿主重载后完成注销；可用「恢复」找回。",
		"purge_confirm":    "彻底删除后无法恢复，确认继续？",
		"remove_confirm":   "移出后该账号不再参与请求，可用「恢复」找回。确认继续？",
		"panel_hint":       "删除/排序等操作也可以直接在 CPAMP 凭证管理里完成。",
		"security":         "安全提示：该页面不经过管理密钥认证，只应在本机或受信网络访问；API Key 不会写入 URL 与日志。",
	},
	"en": {
		"title":            "CommandCode Accounts",
		"subtitle":         "These accounts are the CPA credentials themselves: changes here are visible in CPAMP credential management.",
		"add":              "Add account",
		"api_key":          "CommandCode API key (starts with user_)",
		"label":            "Display name (optional)",
		"base_url":         "Upstream base URL (optional)",
		"save":             "Save",
		"save_hint":        "Saving the same key again never creates a second account.",
		"list":             "Accounts",
		"refresh":          "Refresh",
		"empty":            "No accounts yet. Add one above, or import it in CPAMP credential management.",
		"loading":          "Loading…",
		"status":           "Status",
		"cooldown":         "Cooling down until",
		"enable":           "Enable",
		"disable":          "Disable",
		"edit":             "Edit",
		"remove":           "Remove",
		"restore":          "Restore",
		"purge":            "Delete permanently",
		"removed":          "Removed (recoverable)",
		"write_off":        "Page writes are disabled (allow-page-write=false); this page is read-only.",
		"key_required":     "Enter an API key.",
		"key_prefix":       "The API key must start with user_.",
		"base_url_invalid": "The base URL must start with http:// or https://.",
		"file_required":    "Missing file parameter.",
		"dir_unknown":      "Cannot locate the credential directory (import an account first).",
		"write_disabled":   "Page writes are disabled.",
		"remove_note":      "Removed; the host finishes unregistering after a reload. Use Restore to get it back.",
		"purge_confirm":    "Deleting permanently cannot be undone. Continue?",
		"remove_confirm":   "The account stops serving requests after removal. Continue?",
		"panel_hint":       "Delete and ordering are also available in CPAMP credential management.",
		"security":         "Security: this page is not authenticated by the management key; keep it on localhost or a trusted network. API keys never enter URLs or logs.",
	},
}

// msg returns one localized string, falling back to the other language and then
// to the key itself so a missing entry is visible instead of silent.
func msg(lang, key string) string {
	if table, ok := accountPageCopy[lang]; ok {
		if value, ok := table[key]; ok {
			return value
		}
	}
	for _, table := range accountPageCopy {
		if value, ok := table[key]; ok {
			return value
		}
	}
	return key
}

// AccountsPageHTML renders the account page. It only uses the CSS custom
// properties CPAMP injects into plugin frames, so it follows the panel's theme
// (light/dark) without shipping its own theme.
func AccountsPageHTML(lang string) string {
	t := func(key string) string { return html.EscapeString(msg(lang, key)) }
	other := "en"
	if lang == "en" {
		other = "zh"
	}

	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"")
	b.WriteString(html.EscapeString(lang))
	b.WriteString("\">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n<title>")
	b.WriteString(t("title"))
	b.WriteString("</title>\n<style>\n")
	b.WriteString(pageStyle)
	b.WriteString("</style>\n</head>\n<body>\n")
	b.WriteString("<header class=\"head\">\n  <div>\n    <h1>")
	b.WriteString(t("title"))
	b.WriteString("</h1>\n    <p class=\"muted\">")
	b.WriteString(t("subtitle"))
	b.WriteString("</p>\n  </div>\n  <div class=\"head-actions\">\n    <a class=\"btn ghost\" href=\"?lang=")
	b.WriteString(html.EscapeString(other))
	b.WriteString("\">")
	b.WriteString(html.EscapeString(other))
	b.WriteString("</a>\n    <button class=\"btn ghost\" id=\"refresh\">")
	b.WriteString(t("refresh"))
	b.WriteString("</button>\n  </div>\n</header>\n")

	b.WriteString("<section class=\"card\">\n  <h2>")
	b.WriteString(t("add"))
	b.WriteString("</h2>\n  <div class=\"grid\">\n")
	b.WriteString("    <label>")
	b.WriteString(t("api_key"))
	b.WriteString("<input id=\"apiKey\" type=\"password\" autocomplete=\"off\" spellcheck=\"false\" placeholder=\"user_...\"></label>\n")
	b.WriteString("    <label>")
	b.WriteString(t("label"))
	b.WriteString("<input id=\"label\" type=\"text\" autocomplete=\"off\"></label>\n")
	b.WriteString("    <label>")
	b.WriteString(t("base_url"))
	b.WriteString("<input id=\"baseUrl\" type=\"text\" autocomplete=\"off\" placeholder=\"https://api.commandcode.ai\"></label>\n")
	b.WriteString("    <div class=\"actions\"><button class=\"btn primary\" id=\"save\">")
	b.WriteString(t("save"))
	b.WriteString("</button></div>\n  </div>\n  <p class=\"muted small\" id=\"saveHint\">")
	b.WriteString(t("save_hint"))
	b.WriteString("</p>\n  <p class=\"muted small\" id=\"writeOff\" hidden>")
	b.WriteString(t("write_off"))
	b.WriteString("</p>\n</section>\n")

	b.WriteString("<section class=\"card\">\n  <div class=\"section-head\"><h2>")
	b.WriteString(t("list"))
	b.WriteString("</h2><span class=\"muted small\" id=\"count\"></span></div>\n")
	b.WriteString("  <div id=\"state\" class=\"state\">")
	b.WriteString(t("loading"))
	b.WriteString("</div>\n  <div id=\"accounts\" class=\"accounts\"></div>\n</section>\n")

	b.WriteString("<section class=\"card\">\n  <p class=\"muted small\">")
	b.WriteString(t("panel_hint"))
	b.WriteString("</p>\n  <p class=\"muted small\">")
	b.WriteString(t("security"))
	b.WriteString("</p>\n</section>\n")

	b.WriteString("<div id=\"toast\" class=\"toast\" hidden></div>\n")
	b.WriteString("<script>\nwindow.__CC = ")
	b.WriteString(pageScriptLiteral(lang))
	b.WriteString(";\n</script>\n<script>\n")
	b.WriteString(pageRuntime)
	b.WriteString("\n</script>\n</body>\n</html>\n")
	return b.String()
}

const pageStyle = `
:root { color-scheme: light dark; }
* { box-sizing: border-box; }
body {
  margin: 0; padding: 20px 22px 40px;
  background: var(--app-bg, #eff2f7);
  color: var(--app-text-primary, #2c3e50);
  font: 14px/1.5 -apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
}
h1 { font-size: 20px; margin: 0 0 4px; }
h2 { font-size: 15px; margin: 0 0 12px; }
.head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 16px; }
.head-actions { display: flex; gap: 8px; }
.muted { color: var(--app-text-muted, #8b95a6); margin: 0; }
.small { font-size: 12px; }
.card {
  background: var(--app-surface, #fff);
  border: 1px solid var(--app-border, rgba(15,23,42,.08));
  border-radius: var(--app-radius-md, 12px);
  padding: 16px; margin-bottom: 14px;
}
.grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 12px; align-items: end; }
label { display: flex; flex-direction: column; gap: 6px; font-size: 12px; color: var(--app-text-regular, #5f6c7b); }
input {
  height: 34px; padding: 0 10px; border-radius: 8px;
  border: 1px solid var(--app-border, rgba(15,23,42,.14));
  background: var(--bg-primary, var(--app-surface, #fff));
  color: inherit; font: inherit;
}
input:focus { outline: 2px solid color-mix(in srgb, var(--primary-color, #409eff) 22%, transparent); border-color: var(--primary-color, #409eff); }
.actions { display: flex; gap: 8px; }
.btn {
  height: 34px; padding: 0 14px; border-radius: 8px; cursor: pointer;
  border: 1px solid var(--app-border, rgba(15,23,42,.14));
  background: var(--app-surface, #fff); color: inherit; font: inherit;
}
.btn.primary { background: var(--primary-color, #409eff); border-color: var(--primary-color, #409eff); color: var(--primary-contrast, #fff); }
.btn.danger { color: var(--danger-color, #f56c6c); border-color: color-mix(in srgb, var(--danger-color, #f56c6c) 40%, transparent); }
.btn.ghost { background: transparent; }
.btn:disabled { opacity: .5; cursor: not-allowed; }
.accounts { display: grid; gap: 10px; }
.account {
  display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 12px; align-items: center;
  border: 1px solid var(--app-border, rgba(15,23,42,.08));
  border-radius: 10px; padding: 12px;
  background: var(--bg-secondary, var(--app-surface-muted, #f6faff));
}
.account.off { opacity: .62; }
.account .name { font-weight: 600; display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.account .meta { font-size: 12px; color: var(--app-text-muted, #8b95a6); margin-top: 4px; word-break: break-all; }
.pill { font-size: 11px; padding: 1px 8px; border-radius: 999px; border: 1px solid transparent; }
.pill.ok { color: var(--success-color, #67c23a); border-color: color-mix(in srgb, var(--success-color, #67c23a) 40%, transparent); }
.pill.warn { color: var(--warning-color, #e6a23c); border-color: color-mix(in srgb, var(--warning-color, #e6a23c) 40%, transparent); }
.pill.bad { color: var(--danger-color, #f56c6c); border-color: color-mix(in srgb, var(--danger-color, #f56c6c) 40%, transparent); }
.pill.grey { color: var(--app-text-muted, #8b95a6); border-color: var(--app-border, rgba(15,23,42,.14)); }
.row-actions { display: flex; gap: 8px; flex-wrap: wrap; justify-content: flex-end; }
.state { color: var(--app-text-muted, #8b95a6); padding: 8px 0; }
.toast {
  position: fixed; left: 50%; bottom: 24px; transform: translateX(-50%);
  background: var(--app-surface, #fff); color: inherit;
  border: 1px solid var(--app-border, rgba(15,23,42,.14)); border-radius: 10px;
  padding: 10px 16px; box-shadow: 0 6px 24px rgba(15,23,42,.12); max-width: 80vw;
}
`

var pageRuntime = `
(function () {
  var host = window.__CC || {};
  var S = host.strings || {};
  var state = document.getElementById('state');
  var list = document.getElementById('accounts');
  var toastEl = document.getElementById('toast');
  var writeEnabled = true;

  function toast(text) {
    if (!text) return;
    toastEl.textContent = text;
    toastEl.hidden = false;
    setTimeout(function () { toastEl.hidden = true; }, 4000);
  }

  function request(path, options) {
    var opts = options || {};
    opts.headers = opts.headers || {};
    opts.cache = 'no-store';
    return fetch(path, opts).then(function (res) {
      return res.json().catch(function () { return { ok: false, error: 'HTTP ' + res.status }; });
    });
  }

  function renderRow(account) {
    var row = document.createElement('div');
    row.className = 'account' + (account.disabled || account.removed ? ' off' : '');

    var info = document.createElement('div');
    var name = document.createElement('div');
    name.className = 'name';
    var title = document.createElement('span');
    title.textContent = account.label || account.file;
    name.appendChild(title);

    var pill = document.createElement('span');
    pill.className = 'pill grey';
    var statusText = account.removed ? S.removed : (account.disabled ? S.disable : (account.status || 'active'));
    if (account.removed) pill.className = 'pill grey';
    else if (account.unavailable || (account.status && account.status !== 'active' && account.status !== 'ready')) pill.className = 'pill warn';
    else if (account.disabled) pill.className = 'pill grey';
    else pill.className = 'pill ok';
    pill.textContent = statusText;
    name.appendChild(pill);
    info.appendChild(name);

    var meta = document.createElement('div');
    meta.className = 'meta';
    var bits = [account.file];
    if (account.masked_key) bits.push(account.masked_key);
    if (account.priority) bits.push('priority ' + account.priority);
    if (account.note) bits.push(account.note);
    if (account.next_retry_after) bits.push(S.cooldown + ' ' + account.next_retry_after);
    meta.textContent = bits.join(' · ');
    info.appendChild(meta);
    row.appendChild(info);

    var actions = document.createElement('div');
    actions.className = 'row-actions';

    function button(label, className, handler) {
      var btn = document.createElement('button');
      btn.className = 'btn ' + (className || '');
      btn.textContent = label;
      btn.disabled = !writeEnabled;
      btn.addEventListener('click', function () {
        btn.disabled = true;
        handler().then(function (result) {
          if (result && result.ok === false) toast(result.error || 'failed');
          return load();
        }).finally(function () { btn.disabled = !writeEnabled; });
      });
      return btn;
    }

    if (account.removed) {
      actions.appendChild(button(S.restore, '', function () {
        return request(host.restore + '?file=' + encodeURIComponent(account.file));
      }));
      actions.appendChild(button(S.purge, 'danger', function () {
        if (!window.confirm(S.purge_confirm)) return Promise.resolve(null);
        return request(host.purge + '?file=' + encodeURIComponent(account.file));
      }));
    } else {
      actions.appendChild(button(account.disabled ? S.enable : S.disable, '', function () {
        return request(host.toggle + '?file=' + encodeURIComponent(account.file) + '&disabled=' + (account.disabled ? '0' : '1'));
      }));
      actions.appendChild(button(S.edit, '', function () {
        var key = window.prompt(S.api_key);
        if (!key) return Promise.resolve(null);
        return request(host.save + '?file=' + encodeURIComponent(account.file), {
          headers: { 'X-CommandCode-Api-Key': key }
        });
      }));
      actions.appendChild(button(S.remove, 'danger', function () {
        if (!window.confirm(S.remove_confirm)) return Promise.resolve(null);
        return request(host.remove + '?file=' + encodeURIComponent(account.file));
      }));
    }
    row.appendChild(actions);
    return row;
  }

  function load() {
    return request(host.data).then(function (result) {
      list.innerHTML = '';
      if (!result || result.ok === false) {
        state.textContent = (result && result.error) || 'load failed';
        state.hidden = false;
        return result;
      }
      // The data endpoint returns {accounts, write_enabled}; the plain list path
      // returns the array itself.
      var payload = result.data;
      var accounts = Array.isArray(payload) ? payload : (payload && payload.accounts) || [];
      if (payload && typeof payload.write_enabled === 'boolean') {
        writeEnabled = payload.write_enabled;
        document.getElementById('writeOff').hidden = writeEnabled;
        document.getElementById('save').disabled = !writeEnabled;
      }
      document.getElementById('count').textContent = accounts.length ? String(accounts.length) : '';
      if (!accounts.length) {
        state.textContent = S.empty;
        state.hidden = false;
      } else {
        state.hidden = true;
        accounts.forEach(function (account) { list.appendChild(renderRow(account)); });
      }
      return result;
    });
  }

  document.getElementById('refresh').addEventListener('click', function () { load(); });
  document.getElementById('save').addEventListener('click', function () {
    var key = document.getElementById('apiKey').value.trim();
    var label = document.getElementById('label').value.trim();
    var baseUrl = document.getElementById('baseUrl').value.trim();
    if (!key) { toast(S.key_required); return; }
    var query = '?label=' + encodeURIComponent(label) + '&base_url=' + encodeURIComponent(baseUrl);
    request(host.save + query, { headers: { 'X-CommandCode-Api-Key': key } }).then(function (result) {
      if (result && result.ok === false) { toast(result.error || 'failed'); return; }
      document.getElementById('apiKey').value = '';
      toast(S.saved || 'saved');
      load();
    });
  });

  load();
})();
`

// pageScriptLiteral renders the per-language runtime configuration embedded in
// the page: localized strings plus the endpoint URLs the page calls.
func pageScriptLiteral(lang string) string {
	escape := func(value string) string {
		replaced := strings.ReplaceAll(value, `\`, `\\`)
		replaced = strings.ReplaceAll(replaced, `"`, `\"`)
		return replaced
	}
	keys := []string{
		"api_key", "key_required", "key_prefix", "empty", "status", "cooldown",
		"enable", "disable", "edit", "remove", "restore", "purge", "removed",
		"remove_confirm", "purge_confirm", "save",
	}
	parts := make([]string, 0, len(keys)+6)
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%q:%q", key, escape(msg(lang, key))))
	}
	base := "/v0/resource/plugins/" + ProviderID + "/accounts"
	parts = append(parts,
		fmt.Sprintf("%q:%q", "data", base+"/data"),
		fmt.Sprintf("%q:%q", "save", base+"/save"),
		fmt.Sprintf("%q:%q", "toggle", base+"/toggle"),
		fmt.Sprintf("%q:%q", "remove", base+"/remove"),
		fmt.Sprintf("%q:%q", "restore", base+"/restore"),
		fmt.Sprintf("%q:%q", "purge", base+"/purge"),
	)
	return "{strings:{" + strings.Join(parts, ",") + "}}"
}
