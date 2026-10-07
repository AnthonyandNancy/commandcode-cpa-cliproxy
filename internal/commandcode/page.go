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
		"add_title":        "新增 CommandCode 账号",
		"edit_title":       "编辑 CommandCode 账号",
		"cancel":           "取消",
		"api_key":          "CommandCode API Key（user_ 开头）",
		"api_key_keep":     "留空表示不修改当前 Key",
		"label":            "显示名（可选）",
		"base_url":         "上游地址（可选，默认官方）",
		"save":             "保存",
		"save_hint":        "同一 Key 重复保存不会产生第二条账号。",
		"list":             "账号列表",
		"refresh":          "刷新",
		"empty":            "还没有账号。点击「新增账号」添加，或在 CPAMP 凭证管理中导入。",
		"loading":          "加载中…",
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
		"saved":            "已保存",
		"failed":           "操作失败",
	},
	"en": {
		"title":            "CommandCode Accounts",
		"subtitle":         "These accounts are the CPA credentials themselves: changes here are visible in CPAMP credential management.",
		"add":              "Add account",
		"add_title":        "Add a CommandCode account",
		"edit_title":       "Edit CommandCode account",
		"cancel":           "Cancel",
		"api_key":          "CommandCode API key (starts with user_)",
		"api_key_keep":     "Leave empty to keep the current key",
		"label":            "Display name (optional)",
		"base_url":         "Upstream base URL (optional)",
		"save":             "Save",
		"save_hint":        "Saving the same key again never creates a second account.",
		"list":             "Accounts",
		"refresh":          "Refresh",
		"empty":            "No accounts yet. Use \"Add account\", or import one in CPAMP credential management.",
		"loading":          "Loading…",
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
		"saved":            "Saved",
		"failed":           "The action failed",
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
//
// Every request goes back to this page's own URL with ?action=..., never to a
// sibling path: the menu route is the one resource path every deployment shape
// exposes.
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

	// Header: title on the left, page-level actions on the right.
	b.WriteString("<header class=\"head\">\n  <div class=\"head-text\">\n    <h1>")
	b.WriteString(t("title"))
	b.WriteString("</h1>\n    <p class=\"muted\">")
	b.WriteString(t("subtitle"))
	b.WriteString("</p>\n  </div>\n  <div class=\"head-actions\">\n")
	b.WriteString("    <button class=\"btn primary\" id=\"addAccount\">+ ")
	b.WriteString(t("add"))
	b.WriteString("</button>\n    <button class=\"btn ghost\" id=\"refresh\">")
	b.WriteString(t("refresh"))
	b.WriteString("</button>\n    <a class=\"btn ghost\" href=\"?lang=")
	b.WriteString(html.EscapeString(other))
	b.WriteString("\" title=\"language\">")
	b.WriteString(html.EscapeString(other))
	b.WriteString("</a>\n  </div>\n</header>\n")

	b.WriteString("<p class=\"muted small hint\" id=\"writeOff\" hidden>")
	b.WriteString(t("write_off"))
	b.WriteString("</p>\n")

	// Account list.
	b.WriteString("<section class=\"card\">\n  <div class=\"section-head\"><h2>")
	b.WriteString(t("list"))
	b.WriteString("</h2><span class=\"muted small\" id=\"count\"></span></div>\n")
	b.WriteString("  <div id=\"state\" class=\"state\">")
	b.WriteString(t("loading"))
	b.WriteString("</div>\n  <div id=\"accounts\" class=\"accounts\"></div>\n</section>\n")

	// Footer notes.
	b.WriteString("<section class=\"card\">\n  <p class=\"muted small\">")
	b.WriteString(t("panel_hint"))
	b.WriteString("</p>\n  <p class=\"muted small\">")
	b.WriteString(t("security"))
	b.WriteString("</p>\n</section>\n")

	// Add / edit dialog.
	b.WriteString("<div class=\"overlay\" id=\"overlay\" hidden>\n  <div class=\"modal\" role=\"dialog\" aria-modal=\"true\" aria-labelledby=\"modalTitle\">\n")
	b.WriteString("    <div class=\"modal-head\"><h2 id=\"modalTitle\">")
	b.WriteString(t("add_title"))
	b.WriteString("</h2></div>\n    <div class=\"modal-body\">\n")
	b.WriteString("      <label>")
	b.WriteString(t("api_key"))
	b.WriteString("<input id=\"apiKey\" type=\"password\" autocomplete=\"off\" spellcheck=\"false\" placeholder=\"user_...\"></label>\n")
	b.WriteString("      <label>")
	b.WriteString(t("label"))
	b.WriteString("<input id=\"label\" type=\"text\" autocomplete=\"off\"></label>\n")
	b.WriteString("      <label>")
	b.WriteString(t("base_url"))
	b.WriteString("<input id=\"baseUrl\" type=\"text\" autocomplete=\"off\" placeholder=\"https://api.commandcode.ai\"></label>\n")
	b.WriteString("      <p class=\"muted small\" id=\"modalHint\">")
	b.WriteString(t("save_hint"))
	b.WriteString("</p>\n    </div>\n")
	b.WriteString("    <div class=\"modal-foot\">\n      <button class=\"btn ghost\" id=\"modalCancel\">")
	b.WriteString(t("cancel"))
	b.WriteString("</button>\n      <button class=\"btn primary\" id=\"modalSave\">")
	b.WriteString(t("save"))
	b.WriteString("</button>\n    </div>\n  </div>\n</div>\n")

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
h2 { font-size: 15px; margin: 0; }
.head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 14px; flex-wrap: wrap; }
.head-text { min-width: 260px; flex: 1; }
.head-actions { display: flex; gap: 8px; align-items: center; }
.muted { color: var(--app-text-muted, #8b95a6); margin: 0; }
.small { font-size: 12px; }
.hint { margin: 0 0 12px; }
.card {
  background: var(--app-surface, #fff);
  border: 1px solid var(--app-border, rgba(15,23,42,.08));
  border-radius: var(--app-radius-md, 12px);
  padding: 16px; margin-bottom: 14px;
}
.section-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
label { display: flex; flex-direction: column; gap: 6px; font-size: 12px; color: var(--app-text-regular, #5f6c7b); }
input {
  height: 34px; padding: 0 10px; border-radius: 8px;
  border: 1px solid var(--app-border, rgba(15,23,42,.14));
  background: var(--bg-primary, var(--app-surface, #fff));
  color: inherit; font: inherit; width: 100%;
}
input:focus { outline: 2px solid color-mix(in srgb, var(--primary-color, #409eff) 22%, transparent); border-color: var(--primary-color, #409eff); }
.btn {
  height: 34px; padding: 0 14px; border-radius: 8px; cursor: pointer;
  border: 1px solid var(--app-border, rgba(15,23,42,.14));
  background: var(--app-surface, #fff); color: inherit; font: inherit;
  display: inline-flex; align-items: center; text-decoration: none; white-space: nowrap;
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
.overlay {
  position: fixed; inset: 0; z-index: 40;
  background: rgba(15,23,42,.42);
  display: flex; align-items: center; justify-content: center; padding: 20px;
}
.modal {
  width: min(560px, 100%);
  background: var(--app-surface, #fff); color: inherit;
  border: 1px solid var(--app-border, rgba(15,23,42,.12));
  border-radius: var(--app-radius-md, 12px);
  box-shadow: 0 18px 48px rgba(15,23,42,.24);
  padding: 18px;
}
.modal-head { margin-bottom: 12px; }
.modal-body { display: grid; gap: 12px; }
.modal-body .muted { margin: 0; }
.modal-foot { display: flex; justify-content: flex-end; gap: 8px; margin-top: 18px; }
.toast {
  position: fixed; left: 50%; bottom: 24px; transform: translateX(-50%); z-index: 50;
  background: var(--app-surface, #fff); color: inherit;
  border: 1px solid var(--app-border, rgba(15,23,42,.14)); border-radius: 10px;
  padding: 10px 16px; box-shadow: 0 6px 24px rgba(15,23,42,.12); max-width: 80vw;
}
`

// pageScriptLiteral renders the localized strings embedded into the page. Every
// endpoint is derived from the page's own URL at runtime, so a different mount
// prefix cannot break the page.
func pageScriptLiteral(lang string) string {
	escape := func(value string) string {
		replaced := strings.ReplaceAll(value, `\`, `\\`)
		replaced = strings.ReplaceAll(replaced, `"`, `\"`)
		return replaced
	}
	keys := []string{
		"api_key", "api_key_keep", "key_required", "key_prefix", "label", "base_url",
		"empty", "status", "cooldown", "enable", "disable", "edit", "remove", "restore",
		"purge", "removed", "remove_confirm", "purge_confirm", "save", "saved", "failed",
		"add_title", "edit_title", "loading", "write_disabled",
	}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%q:%q", key, escape(msg(lang, key))))
	}
	return "{strings:{" + strings.Join(parts, ",") + "}}"
}

const pageRuntime = `
(function () {
  var S = ((window.__CC || {}).strings) || {};
  // Every request targets this page's own path with ?action=... : that is the
  // single resource route the host always exposes, whatever the prefix is.
  var base = window.location.pathname.replace(/\/+$/, '');
  var state = document.getElementById('state');
  var list = document.getElementById('accounts');
  var toastEl = document.getElementById('toast');
  var overlay = document.getElementById('overlay');
  var modalTitle = document.getElementById('modalTitle');
  var modalHint = document.getElementById('modalHint');
  var keyInput = document.getElementById('apiKey');
  var labelInput = document.getElementById('label');
  var baseUrlInput = document.getElementById('baseUrl');
  var writeEnabled = true;
  var editingFile = '';

  function toast(text) {
    if (!text) return;
    toastEl.textContent = text;
    toastEl.hidden = false;
    setTimeout(function () { toastEl.hidden = true; }, 4000);
  }

  function call(action, params, options) {
    var query = '?action=' + encodeURIComponent(action);
    if (params) {
      Object.keys(params).forEach(function (key) {
        if (params[key] !== '' && params[key] !== null && params[key] !== undefined) {
          query += '&' + encodeURIComponent(key) + '=' + encodeURIComponent(params[key]);
        }
      });
    }
    var opts = options || {};
    opts.cache = 'no-store';
    return fetch(base + query, opts).then(function (res) {
      return res.text().then(function (text) {
        var parsed = null;
        try { parsed = JSON.parse(text); } catch (e) { parsed = null; }
        if (!parsed) { return { ok: false, error: 'HTTP ' + res.status }; }
        return parsed;
      });
    }, function (err) {
      return { ok: false, error: String((err && err.message) || err) };
    });
  }

  function openModal(account) {
    if (!writeEnabled) { toast(S.write_disabled || S.failed); return; }
    editingFile = account ? account.file : '';
    modalTitle.textContent = account ? S.edit_title : S.add_title;
    keyInput.value = '';
    keyInput.placeholder = account ? S.api_key_keep : 'user_...';
    labelInput.value = account ? (account.label || '') : '';
    baseUrlInput.value = account ? (account.base_url || '') : '';
    modalHint.textContent = S.save_hint || '';
    overlay.hidden = false;
    keyInput.focus();
  }

  function closeModal() {
    overlay.hidden = true;
    editingFile = '';
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
    var statusText = account.removed ? S.removed : (account.disabled ? S.disable : (account.status || 'active'));
    if (account.removed || account.disabled) pill.className = 'pill grey';
    else if (account.unavailable || (account.status && account.status !== 'active' && account.status !== 'ready')) pill.className = 'pill warn';
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
    if (account.next_retry_after) bits.push((S.cooldown || 'cooldown') + ' ' + account.next_retry_after);
    meta.textContent = bits.join(' · ');
    info.appendChild(meta);
    row.appendChild(info);

    var actions = document.createElement('div');
    actions.className = 'row-actions';

    function button(text, className, handler) {
      var btn = document.createElement('button');
      btn.className = 'btn ' + (className || '');
      btn.textContent = text;
      btn.disabled = !writeEnabled;
      btn.addEventListener('click', function () {
        btn.disabled = true;
        Promise.resolve(handler()).then(function (result) {
          if (result && result.ok === false) toast(result.error || S.failed);
          return load();
        }).finally(function () { btn.disabled = !writeEnabled; });
      });
      return btn;
    }

    if (account.removed) {
      actions.appendChild(button(S.restore, '', function () {
        return call('restore', { file: account.file });
      }));
      actions.appendChild(button(S.purge, 'danger', function () {
        if (!window.confirm(S.purge_confirm)) return Promise.resolve(null);
        return call('purge', { file: account.file });
      }));
    } else {
      actions.appendChild(button(account.disabled ? S.enable : S.disable, '', function () {
        return call('toggle', { file: account.file, disabled: account.disabled ? '0' : '1' });
      }));
      actions.appendChild(button(S.edit, '', function () {
        openModal(account);
        return Promise.resolve(null);
      }));
      actions.appendChild(button(S.remove, 'danger', function () {
        if (!window.confirm(S.remove_confirm)) return Promise.resolve(null);
        return call('remove', { file: account.file });
      }));
    }
    row.appendChild(actions);
    return row;
  }

  function load() {
    return call('data').then(function (result) {
      list.innerHTML = '';
      if (!result || result.ok === false) {
        state.textContent = (result && result.error) || 'load failed';
        state.hidden = false;
        return result;
      }
      var payload = result.data;
      var accounts = Array.isArray(payload) ? payload : (payload && payload.accounts) || [];
      if (payload && typeof payload.write_enabled === 'boolean') {
        writeEnabled = payload.write_enabled;
        document.getElementById('writeOff').hidden = writeEnabled;
        document.getElementById('addAccount').disabled = !writeEnabled;
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
  document.getElementById('addAccount').addEventListener('click', function () { openModal(null); });
  document.getElementById('modalCancel').addEventListener('click', closeModal);
  overlay.addEventListener('click', function (event) { if (event.target === overlay) closeModal(); });
  document.addEventListener('keydown', function (event) { if (event.key === 'Escape' && !overlay.hidden) closeModal(); });

  document.getElementById('modalSave').addEventListener('click', function () {
    var key = keyInput.value.trim();
    var label = labelInput.value.trim();
    var baseUrl = baseUrlInput.value.trim();
    if (!editingFile && !key) { toast(S.key_required); return; }
    if (key && key.indexOf('user_') !== 0) { toast(S.key_prefix); return; }
    var headers = {};
    if (key) headers['X-CommandCode-Api-Key'] = key;
    call('save', { file: editingFile, label: label, base_url: baseUrl }, { headers: headers }).then(function (result) {
      if (result && result.ok === false) { toast(result.error || S.failed); return; }
      closeModal();
      toast(S.saved || 'saved');
      load();
    });
  });

  load();
})();
`
