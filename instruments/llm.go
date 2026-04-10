// instruments/llm.go
//
// LLM instrument – renders the chat UI.
//
// WASI preview1 has no socket support, so all actual HTTP calls to the
// LLM API are handled by the WASIO host (see /_llm/* endpoints in main.go).
// This module's only job is to emit the full-page HTML chat interface.
//
// Configuration lives in config.json under the /llm route's "env" block:
//   OPENAI_BASE_URL  – forwarded to host handler, not used here
//   OPENAI_API_KEY   – forwarded to host handler, not used here
//   OPENAI_MODEL     – forwarded to host handler, not used here
//   OPENAI_SYSTEM    – forwarded to host handler, not used here

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Payload struct {
	Params map[string]string `json:"params"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 512*1024), 512*1024)
	var sb strings.Builder
	for scanner.Scan() {
		sb.WriteString(scanner.Text())
	}

	var payload Payload
	if err := json.Unmarshal([]byte(sb.String()), &payload); err != nil {
		fmt.Println(`{"error":"invalid payload"}`)
		return
	}

	op := payload.Params["op"]
	if op == "" || op == "ui" {
		handleUI()
		return
	}
	fmt.Printf(`{"error":"unknown operation %q – use the native /_llm/* endpoints for chat and models"}`, op)
}


// ── UI ────────────────────────────────────────────────────────────────────────

// handleUI renders the full-page chat interface.
// NOTE: Go raw strings (backtick-delimited) terminate at the first backtick
// character, so JS template literals and regex backticks are injected via
// string concatenation using "\x60" (= backtick) instead.
func handleUI() {
	bt := "\x60"      // single backtick
	tbt := bt + bt + bt // triple backtick (for fenced-code regex)

	// Part 1: HTML head, CSS, navbar, settings, chatbox, inputbar, script preamble
	// – up to and including the renderMd function header.
	p1 := `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>WASIO · LLM Chat</title>
  <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.2/dist/css/bootstrap.min.css" rel="stylesheet">
  <style>
    body { background: #f0f2f5; display: flex; flex-direction: column; height: 100vh; margin: 0; }
    main { flex: 1; overflow: hidden; display: flex; flex-direction: column; max-width: 820px; width: 100%; margin: 0 auto; padding: .75rem; gap: .75rem; }
    #chat-box { flex: 1; overflow-y: auto; background: #fff; border-radius: 12px; padding: 1rem; box-shadow: 0 2px 8px rgba(0,0,0,.08); }
    .row-user { display:flex; flex-direction:column; align-items:flex-end; margin-bottom:.5rem; }
    .row-asst { display:flex; flex-direction:column; align-items:flex-start; margin-bottom:.5rem; }
    .lbl { font-size:.68em; opacity:.55; margin-bottom:2px; }
    .bubble { max-width:78%; border-radius:14px; padding:.55rem .9rem; word-break:break-word; white-space:pre-wrap; line-height:1.5; }
    .bubble-user { background:#0d6efd; color:#fff; }
    .bubble-asst { background:#e9ecef; color:#212529; }
    .bubble-error { background:#f8d7da; color:#842029; }
    code { background:rgba(0,0,0,.08); border-radius:3px; padding:0 3px; font-size:.88em; }
    pre  { background:rgba(0,0,0,.06); border-radius:6px; padding:.5rem .75rem; overflow-x:auto; }
    #input-bar { background:#fff; border-radius:12px; padding:.75rem; box-shadow:0 2px 8px rgba(0,0,0,.08); }
    #spinner { display:none; }
    .settings-toggle { font-size:.85rem; }
  </style>
</head>
<body>
<nav class="navbar navbar-dark bg-primary py-1">
  <div class="container-fluid">
    <span class="navbar-brand mb-0 h6"><strong>WASIO</strong> · LLM Chat</span>
    <span class="text-white-50 small me-2" id="model-badge">connecting…</span>
    <a class="btn btn-outline-light btn-sm" href="/">← Home</a>
  </div>
</nav>

<main>
  <!-- Settings -->
  <div>
    <button class="btn btn-sm btn-outline-secondary settings-toggle" type="button"
            data-bs-toggle="collapse" data-bs-target="#settings">⚙ Settings</button>
    <div class="collapse mt-2" id="settings">
      <div class="card card-body py-2">
        <div class="row g-2 align-items-end">
          <div class="col-md-5">
            <label class="form-label mb-1 small">Model</label>
            <select id="sel-model" class="form-select form-select-sm"></select>
          </div>
          <div class="col-md-3">
            <label class="form-label mb-1 small">Temperature <span id="lbl-temp">0.7</span></label>
            <input type="range" class="form-range" id="inp-temp" min="0" max="2" step="0.05" value="0.7"
                   oninput="document.getElementById('lbl-temp').textContent=parseFloat(this.value).toFixed(2)">
          </div>
          <div class="col-md-2">
            <label class="form-label mb-1 small">Max tokens</label>
            <input type="number" class="form-control form-control-sm" id="inp-maxtok" placeholder="∞" min="1" max="32000">
          </div>
          <div class="col-md-2 d-flex align-items-end">
            <button class="btn btn-sm btn-outline-danger w-100" onclick="clearChat()">🗑 Clear</button>
          </div>
          <div class="col-12">
            <label class="form-label mb-1 small">System prompt</label>
            <textarea class="form-control form-control-sm" id="inp-system" rows="2">You are a helpful assistant.</textarea>
          </div>
        </div>
      </div>
    </div>
  </div>

  <!-- Chat window -->
  <div id="chat-box">
    <div class="row-asst">
      <div class="lbl">assistant</div>
      <div class="bubble bubble-asst">👋 Hi! I'm powered by an OpenAI-compatible LLM. Ask me anything!</div>
    </div>
  </div>

  <!-- Input bar -->
  <div id="input-bar">
    <div class="d-flex gap-2">
      <textarea class="form-control" id="inp-msg" rows="2"
                placeholder="Type a message… (Enter = send, Shift+Enter = newline)"></textarea>
      <div class="d-flex flex-column gap-1 justify-content-center">
        <button class="btn btn-primary px-3" id="btn-send" onclick="sendMessage()">Send</button>
        <div id="spinner" class="text-center pt-1">
          <div class="spinner-border spinner-border-sm text-primary"></div>
        </div>
      </div>
    </div>
    <div class="mt-1 text-muted" style="font-size:.72em">
      Turns: <span id="cnt-turns">0</span> &nbsp;|&nbsp;
      Total tokens: <span id="cnt-tokens">0</span>
    </div>
  </div>
</main>

<script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.2/dist/js/bootstrap.bundle.min.js"></script>
<script>
let history = [];
let totalTokens = 0;

async function loadModels() {
  try {
    const r = await fetch('/_llm/models');
    const data = await r.json();
    const sel = document.getElementById('sel-model');
    const models = data.models || [];
    sel.innerHTML = models.length
      ? models.map(function(m){ return '<option value="'+esc(m)+'">'+esc(m)+'</option>'; }).join('')
      : '<option value="">auto-detect</option>';
    document.getElementById('model-badge').textContent = models[0] || 'auto-detect';
  } catch {
    document.getElementById('sel-model').innerHTML = '<option value="">auto-detect</option>';
    document.getElementById('model-badge').textContent = 'offline?';
  }
}

function esc(s) {
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

// Very light markdown: fenced code blocks, inline code, bold
function renderMd(text) {
  let s = esc(text);
  // Fenced code blocks
  s = s.replace(/` + tbt + `[\w]*\n?([\s\S]*?)` + tbt + `/g, '<pre><code>$1</code></pre>');
  // Inline code
  s = s.replace(/` + bt + `([^` + bt + `\n]+)` + bt + `/g, '<code>$1</code>');
  // Bold
  s = s.replace(/\*\*(.*?)\*\*/g, '<strong>$1</strong>');
  return s;
}

function addBubble(role, content, isError) {
  const box = document.getElementById('chat-box');
  const row = document.createElement('div');
  row.className = role === 'user' ? 'row-user' : 'row-asst';
  const cls = isError ? 'bubble-error' : (role === 'user' ? 'bubble-user' : 'bubble-asst');
  row.innerHTML = '<div class="lbl">'+esc(role)+'</div><div class="bubble '+cls+'">'+renderMd(content)+'</div>';
  box.appendChild(row);
  box.scrollTop = box.scrollHeight;
}

async function sendMessage() {
  const inp = document.getElementById('inp-msg');
  const message = inp.value.trim();
  if (!message) return;

  inp.value = '';
  addBubble('user', message);
  document.getElementById('btn-send').disabled = true;
  document.getElementById('spinner').style.display = 'block';

  const params = new URLSearchParams({
    message,
    system:      document.getElementById('inp-system').value,
    temperature: document.getElementById('inp-temp').value,
    history:     JSON.stringify(history),
  });
  const model = document.getElementById('sel-model').value;
  if (model) params.set('model', model);
  const mt = document.getElementById('inp-maxtok').value;
  if (mt) params.set('max_tokens', mt);

  try {
    const r = await fetch('/_llm/chat?' + params.toString());
    const text = await r.text();
    let content = '', usedModel = '';
    try {
      const data = JSON.parse(text);
      if (data.error) {
        addBubble('assistant', '\u26a0 ' + data.error, true);
      } else {
        content = data.content || '';
        usedModel = data.model || '';
        if (data.usage && data.usage.total_tokens) {
          totalTokens += data.usage.total_tokens;
          document.getElementById('cnt-tokens').textContent = totalTokens;
        }
        addBubble('assistant', content);
      }
    } catch {
      content = text;
      addBubble('assistant', text);
    }

    if (content) {
      history.push({ role: 'user',      content: message });
      history.push({ role: 'assistant', content });
      // Keep last 20 exchanges to avoid URL length issues
      if (history.length > 40) history = history.slice(history.length - 40);
      document.getElementById('cnt-turns').textContent = Math.floor(history.length / 2);
    }
    if (usedModel) {
      document.getElementById('model-badge').textContent = usedModel;
      const sel = document.getElementById('sel-model');
      if ([...sel.options].every(o => o.value !== usedModel)) {
        sel.innerHTML += '<option value="'+esc(usedModel)+'" selected>'+esc(usedModel)+'</option>';
      } else {
        sel.value = usedModel;
      }
    }
  } catch (e) {
    addBubble('assistant', '\u26a0 Network error: ' + e.message, true);
  } finally {
    document.getElementById('btn-send').disabled = false;
    document.getElementById('spinner').style.display = 'none';
    inp.focus();
  }
}

function clearChat() {
  history = []; totalTokens = 0;
  document.getElementById('chat-box').innerHTML =
    '<div class="row-asst"><div class="lbl">assistant</div><div class="bubble bubble-asst">Conversation cleared. How can I help you?</div></div>';
  document.getElementById('cnt-turns').textContent  = '0';
  document.getElementById('cnt-tokens').textContent = '0';
}

document.getElementById('inp-msg').addEventListener('keydown', e => {
  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); sendMessage(); }
});
document.getElementById('sel-model').addEventListener('change', function() {
  document.getElementById('model-badge').textContent = this.value || 'auto-detect';
});

loadModels();
</script>
</body>
</html>`

	fmt.Print(p1)
}
