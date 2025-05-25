package main

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"
)

/* ------------------------------------------------------------------ */
/* Config & Types                                                     */
/* ------------------------------------------------------------------ */

const (
	chatDir   = "/chat" // guest mount (mapped to host "./chat")
	msgFile   = chatDir + "/messages.json"
	maxStored = 100 // absolute cap on stored messages
)

type Message struct {
	Timestamp int64  `json:"timestamp"`
	Username  string `json:"username"`
	Text      string `json:"text"`
}

type Payload struct {
	Params map[string]string `json:"params"`
}

/* ------------------------------------------------------------------ */
/* Main                                                               */
/* ------------------------------------------------------------------ */

func main() {
	ensureDir(chatDir)

	var pl Payload
	if err := json.NewDecoder(os.Stdin).Decode(&pl); err != nil {
		// If there is no valid JSON payload, just serve the UI
		serveUI()
		return
	}

	switch strings.ToLower(pl.Params["action"]) {
	case "send":
		handleSend(pl.Params)
	case "ui", "":
		serveUI()
	default: // "get" or anything else falls through to JSON list
		handleGet(pl.Params)
	}
}

/* ------------------------------------------------------------------ */
/* Action: send                                                       */
/* ------------------------------------------------------------------ */

func handleSend(p map[string]string) {
	name := strings.TrimSpace(p["username"])
	if name == "" {
		name = "Anonymous"
	}
	text := strings.TrimSpace(p["text"])
	if text == "" {
		writeJSON(map[string]string{"error": "Message is empty"})
		return
	}

	msg := Message{Timestamp: time.Now().Unix(), Username: name, Text: text}

	if err := appendMessage(msg); err != nil {
		writeJSON(map[string]string{"error": "Write failed"})
		return
	}
	writeJSON(map[string]string{"status": "ok"})
}

/* ------------------------------------------------------------------ */
/* Action: get (default)                                              */
/* ------------------------------------------------------------------ */

func handleGet(p map[string]string) {
	limit := 50
	if n, _ := strconv.Atoi(p["n"]); n > 0 && n <= maxStored {
		limit = n
	}

	msgs, err := readMessages()
	if err != nil {
		writeJSON([]Message{}) // empty list on error
		return
	}
	if len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	writeJSON(msgs)
}

/* ------------------------------------------------------------------ */
/* Persistence helpers                                                */
/* ------------------------------------------------------------------ */

// appendMessage loads the file, appends, trims, then atomically writes back
func appendMessage(m Message) error {
	msgs, _ := readMessages() // treat error as empty chat
	msgs = append(msgs, m)
	if len(msgs) > maxStored {
		msgs = msgs[len(msgs)-maxStored:]
	}

	data, _ := json.MarshalIndent(msgs, "", "  ")

	tmp := msgFile + ".tmp"
	if err := ioutil.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, msgFile)
}

func readMessages() ([]Message, error) {
	data, err := ioutil.ReadFile(msgFile)
	if os.IsNotExist(err) {
		return []Message{}, nil
	}
	if err != nil {
		return nil, err
	}

	var msgs []Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

func ensureDir(path string) {
	_ = os.MkdirAll(path, 0o755)
}

/* ------------------------------------------------------------------ */
/* Output helpers                                                     */
/* ------------------------------------------------------------------ */

func writeJSON(v interface{}) {
	out, _ := json.Marshal(v)
	os.Stdout.Write(out)
}

func serveUI() {
	os.Stdout.Write([]byte(uiHTML))
}

/* ------------------------------------------------------------------ */
/* Embedded UI                                                        */
/* ------------------------------------------------------------------ */

const uiHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>WASIO Realtime Chat</title>
  <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.2/dist/css/bootstrap.min.css" rel="stylesheet" />
  <style>
    body { max-width: 600px; margin: 2rem auto; font-family: system-ui, sans-serif; }
    #chatBox { height: 400px; overflow-y: auto; border: 1px solid #ccc; border-radius: .25rem;
               padding: 1rem; background: #f8f9fa; }
    .message { margin-bottom: .75rem; }
    .timestamp { font-size: .8rem; color: #6c757d; }
    .username { font-weight: 600; color: #0d6efd; }
  </style>
</head>
<body>
<h2 class="mb-4 text-center">WASIO Realtime Chat</h2>

<div id="chatBox" aria-live="polite" aria-relevant="additions text"></div>

<form id="chatForm" class="mt-3 d-flex gap-2" autocomplete="off">
  <input id="username" class="form-control" placeholder="Your name" required minlength="2">
  <input id="message"  class="form-control" placeholder="Enter message..." required minlength="1">
  <button class="btn btn-primary">Send</button>
</form>

<script>
const chatBox = document.getElementById('chatBox');
const chatForm = document.getElementById('chatForm');
const username = document.getElementById('username');
const message  = document.getElementById('message');

function esc(s){return s.replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));}
function fmt(ts){return new Date(ts*1000).toLocaleTimeString([], {hour12:false});}

async function fetchMsgs(){
  try{
    const r = await fetch('/chat?action=get&n=50');
    const data = await r.json();
    chatBox.innerHTML='';
    data.forEach(m=>{
      chatBox.insertAdjacentHTML('beforeend',
        '<div class="message"><span class="timestamp">'+esc(fmt(m.timestamp))+'</span> '+
        '<span class="username">'+esc(m.username)+'</span>: '+
        '<span>'+esc(m.text)+'</span></div>');
    });
    chatBox.scrollTop=chatBox.scrollHeight;
  }catch(e){console.error(e);}
}

chatForm.addEventListener('submit',async e=>{
  e.preventDefault();
  const params=new URLSearchParams({action:'send',username:username.value.trim(),text:message.value.trim()});
  await fetch('/chat?'+params.toString());
  message.value='';
  fetchMsgs();
});

fetchMsgs();
setInterval(fetchMsgs, 2000);
</script>
</body>
</html>`
