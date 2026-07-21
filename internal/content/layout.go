package content

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
)

// layout wraps rendered lesson HTML in a full page that is Play-With-Docker
// compatible in the way that matters: it embeds live xterm.js terminal
// panels and wires quizzes/exercises to the platform's own APIs — the
// equivalent of the legacy pwd.newSession + quiz.js + exercises.js glue, but
// talking to this binary's endpoints (/api/v1/sessions, /terminals/,
// /api/v1/challenges).
//
// The lesson's front-matter image boots the session instances (one per
// terminal; `terms:` picks how many, 0–6, default 1 — same contract as the
// legacy writing-tutorials.md). The terminal emulator is the vendored
// xterm.js served under assets/ (see assets.go / MANIFEST).
func layout(fm FrontMatter, body string) string {
	title := fm.Title
	if title == "" {
		title = "training lesson"
	}
	terms := 1
	if fm.Terms != nil {
		terms = *fm.Terms
		if terms < 0 {
			terms = 0
		}
		if terms > 6 {
			terms = 6
		}
	}
	console := ""
	mainClass := "solo"
	if terms > 0 {
		console = consoleTmpl
		mainClass = "split"
	}
	// Resolve one image per terminal: term_images positionally, falling back
	// to the lesson's image: wherever it's absent or blank. Doing it here
	// means the page never has to know the fallback rule.
	images := make([]string, terms)
	for i := range images {
		images[i] = fm.Image
		if i < len(fm.TermImages) && strings.TrimSpace(fm.TermImages[i]) != "" {
			images[i] = strings.TrimSpace(fm.TermImages[i])
		}
	}
	imagesJSON, err := json.Marshal(images)
	if err != nil { // a []string always marshals; keep the page renderable
		imagesJSON = []byte("[]")
	}

	page := strings.Replace(pageTmpl, "__CONSOLE__", console, 1)
	return fmt.Sprintf(page, html.EscapeString(title), mainClass, body, fm.Image, terms, string(imagesJSON))
}

// consoleTmpl is the console column: session controls plus a #terms holder
// the script fills with one xterm panel per terminal.
const consoleTmpl = `<div class="col">
    <section class="card"><h2>Console (play with docker)</h2><div class="body">
      <div class="row">
        <button id="boot">Start session</button>
        <button id="stop" class="ghost" hidden>Stop</button>
        <span class="pill" id="tstatus">idle</span>
        <span class="pill" id="texpiry"></span>
      </div>
      <div id="terms"></div>
    </div></section>
  </div>`

// Template arguments: %s title, %s main class, %s body, %q image, %d terms,
// %s per-terminal images (JSON array).
const pageTmpl = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<link rel="stylesheet" href="assets/xterm.css">
<script src="assets/xterm.js"></script>
<script src="assets/xterm-addon-fit.js"></script>
<style>
  :root { color-scheme: light dark; --bg:#0f1117; --panel:#171a23; --fg:#e6e8ee; --muted:#9aa3b2; --accent:#4f8cff; --ok:#31c48d; --bad:#f05252; --border:#262b38; }
  * { box-sizing:border-box; }
  [hidden]{ display:none !important; }
  body { margin:0; font:15px/1.6 -apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif; background:var(--bg); color:var(--fg); }
  header { padding:14px 24px; border-bottom:1px solid var(--border); display:flex; gap:14px; align-items:center; }
  header a { color:var(--accent); text-decoration:none; font-size:13px; }
  main { display:grid; gap:20px; padding:24px; max-width:1200px; margin:0 auto; }
  main.split { grid-template-columns:1fr 1fr; }
  main.solo { grid-template-columns:1fr; max-width:800px; }
  @media (max-width:900px){ main.split{ grid-template-columns:1fr; } }
  .col{ display:grid; gap:20px; align-content:start; }
  .card{ background:var(--panel); border:1px solid var(--border); border-radius:10px; overflow:hidden; }
  .card h2{ font-size:12px; text-transform:uppercase; letter-spacing:.08em; color:var(--muted); margin:0; padding:12px 16px; border-bottom:1px solid var(--border); }
  .card .body{ padding:16px; }
  article h1{ font-size:22px; margin:.1em 0 .4em; }
  article h2{ font-size:17px; margin:1em 0 .3em; }
  article code{ background:#0b0d13; padding:2px 6px; border-radius:4px; font-family:ui-monospace,Menlo,monospace; font-size:13px; }
  article pre{ background:#0b0d13; padding:12px; border-radius:6px; overflow:auto; }
  article pre code{ background:none; padding:0; }
  article pre.term-code{ cursor:pointer; border:1px solid var(--border); position:relative; }
  article pre.term-code:hover{ border-color:var(--accent); }
  article pre.term-code::after{ content:"▶ term" attr(data-term); position:absolute; top:6px; right:8px; font-size:11px; color:var(--muted); font-family:inherit; }
  .termwrap{ margin-bottom:14px; }
  .termhead{ display:flex; gap:8px; align-items:center; font-size:12px; color:var(--muted); margin:0 0 6px; }
  .termbox{ background:#0b0d13; border:1px solid var(--border); border-radius:6px; padding:6px; height:300px; }
  /* The .term div is the fit addon's measuring box: it must have a definite
     size. xterm v5 renders its own element as .xterm inside it (NOT .terminal,
     the old v2/v3 class), so fill both. */
  .termbox .term{ height:100%%; width:100%%; }
  .termbox .xterm{ height:100%%; }
  .quiz label,.exercise{ display:block; }
  .quiz label{ padding:8px 10px; border:1px solid var(--border); border-radius:6px; margin:6px 0; cursor:pointer; }
  .quiz label:hover{ border-color:var(--accent); }
  .quiz .q,.exercise .q{ font-weight:600; margin-bottom:8px; }
  button,.exercise-demo{ background:var(--accent); color:#fff; border:0; border-radius:6px; padding:8px 14px; font-size:13px; cursor:pointer; text-decoration:none; display:inline-block; }
  button.ghost{ background:transparent; border:1px solid var(--border); color:var(--muted); }
  .verdict{ margin-top:10px; font-weight:600; }
  .row{ display:flex; gap:8px; align-items:center; margin-bottom:8px; flex-wrap:wrap; }
  .pill{ font-size:11px; color:var(--muted); }
</style>
</head>
<body>
<header><strong>training-platform</strong> <a href="/">lessons</a> <a href="/scoreboard">scoreboard</a></header>
<main class="%s">
  <div class="col">
    <section class="card"><h2>Lesson</h2><div class="body"><article>%s</article></div></section>
  </div>
  __CONSOLE__
</main>
<script>
const lessonImage = %q;
const TERMS = %d;
// One image per terminal (term_images, falling back to the lesson image).
const TERM_IMAGES = %s;

// ---------------------------------------------------------------------------
// Console: one session instance (Pod) per terminal, PWD "node" semantics.
// Lifecycle handled here: boot -> attach -> keepalive ping -> reconnect on
// drop -> explicit stop; pod names persist in sessionStorage so a reload
// reattaches to the running instances instead of leaking new Pods.
// ---------------------------------------------------------------------------
const store = { key: 'tp-session:' + location.pathname,
  load(){ try { return JSON.parse(sessionStorage.getItem(this.key)); } catch(e){ return null; } },
  save(pods){ sessionStorage.setItem(this.key, JSON.stringify({pods, images: TERM_IMAGES})); },
  clear(){ sessionStorage.removeItem(this.key); } };

const nodes = [];      // [{name, pod, ip, term, fit, ws, retries, stopped}]
let keepaliveTimer = null;
let routerHost = null; // from /api/v1/config, for exposed-port links

const $ = (s)=>document.querySelector(s);
const esc = (s)=>String(s).replace(/[&<>"]/g, c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));
const setStatus = (t, ok)=>{ const el=$('#tstatus'); if(el){ el.textContent=t; el.style.color = ok?'var(--ok)':'var(--muted)'; } };

function buildPanels(){
  const holder = $('#terms');
  if(!holder) return;
  holder.innerHTML = '';
  for(let n=1; n<=TERMS; n++){
    const wrap = document.createElement('div');
    wrap.className = 'termwrap';
    // Label the node with its image when the lesson mixes them — with
    // node1 on nginx and node2 on busybox, "node2" alone tells you nothing.
    const img = TERM_IMAGES[n-1] || lessonImage;
    const mixed = TERM_IMAGES.some(i => i !== TERM_IMAGES[0]);
    const imgTag = (mixed && img) ? ' <span class="pill">'+esc(img)+'</span>' : '';
    wrap.innerHTML = '<div class="termhead">node'+n+imgTag+' <span class="pill nstatus">idle</span></div>'+
                     '<div class="termbox"><div class="term term'+n+'"></div></div>';
    holder.appendChild(wrap);
    nodes.push({ name:'node'+n, el:wrap, image:(TERM_IMAGES[n-1]||lessonImage),
                 pod:null, ip:null, term:null, fit:null, ro:null, ws:null, retries:0, stopped:false });
  }
}

function nodeStatus(node, text, ok){
  const el = node.el.querySelector('.nstatus');
  if(el){ el.textContent = text; el.style.color = ok?'var(--ok)':'var(--muted)'; }
}

function makeTerm(node){
  if(node.term) return;
  node.term = new Terminal({ cursorBlink:true, fontSize:13, theme:{ background:'#0b0d13' } });
  node.fit = new FitAddon.FitAddon();
  node.term.loadAddon(node.fit);
  node.term.open(node.el.querySelector('.term'));
  node.term.onData(d => { if(node.ws && node.ws.readyState===1) node.ws.send(new TextEncoder().encode(d)); });
  node.term.onResize(({cols, rows}) => {
    if(node.ws && node.ws.readyState===1) node.ws.send(JSON.stringify({type:'resize', cols, rows}));
  });
  // Fit once the element is laid out, and again whenever the panel resizes
  // (grid column reflow, window resize, devtools open) — the panel width is
  // only known after layout, so an open-time fit alone leaves it mis-sized.
  fitNode(node);
  node.ro = new ResizeObserver(() => fitNode(node));
  node.ro.observe(node.el.querySelector('.termbox'));
}

// fitNode resizes the terminal to its container on the next frame (so the
// browser has applied layout) and reports the size to the server TTY.
function fitNode(node){
  requestAnimationFrame(() => {
    if(!node.fit || !node.term) return;
    try { node.fit.fit(); } catch(e){ return; }
    if(node.ws && node.ws.readyState===1)
      node.ws.send(JSON.stringify({type:'resize', cols:node.term.cols, rows:node.term.rows}));
  });
}

function connect(node){
  const proto = location.protocol==='https:' ? 'wss' : 'ws';
  const ws = new WebSocket(proto+'://'+location.host+'/terminals/'+node.pod);
  ws.binaryType = 'arraybuffer';
  node.ws = ws;
  ws.onopen = () => {
    node.retries = 0;
    nodeStatus(node, node.pod, true);
    fitNode(node); // re-fit + push the size now that the TTY exists
    node.term.focus();
  };
  ws.onmessage = (e) => node.term.write(typeof e.data==='string' ? e.data : new Uint8Array(e.data));
  ws.onclose = async () => {
    if(node.stopped) return;
    nodeStatus(node, 'disconnected', false);
    // Reconnect while the Pod is still alive (a dropped socket is not a
    // dead session); give up once the Pod is gone or after a few tries.
    if(node.retries++ < 5){
      try {
        const r = await fetch('/api/v1/sessions/'+node.pod);
        if(r.ok && (await r.json()).ready){
          nodeStatus(node, 'reconnecting…', false);
          setTimeout(()=>connect(node), 1500*node.retries);
          return;
        }
      } catch(e){ /* fall through */ }
    }
    nodeStatus(node, 'session ended', false);
  };
}

async function bootNode(node){
  nodeStatus(node, 'starting…', false);
  const r = await fetch('/api/v1/sessions', { method:'POST',
    headers:{'Content-Type':'application/json'}, body:JSON.stringify({image:node.image}) });
  if(!r.ok){
    let msg = 'start failed ('+r.status+')';
    try { const j = await r.json(); if(j.error) msg = j.error; } catch(e){}
    nodeStatus(node, msg, false);
    throw new Error(msg);
  }
  const j = await r.json();
  node.pod = j.pod; node.ip = j.ip;
  makeTerm(node);
  connect(node);
  return j.expires_at;
}

async function reattach(saved){
  // A reload reuses running Pods: state inside them (files, containers)
  // survives; only the shell is new. Falls back to a fresh boot when any
  // Pod is gone or the lesson image changed.
  // Reattach only to pods booted from the same images — editing a lesson's
  // images must not silently reuse sandboxes built from the old ones.
  if(!saved || !Array.isArray(saved.pods) || saved.pods.length !== TERMS) return false;
  if(JSON.stringify(saved.images) !== JSON.stringify(TERM_IMAGES)) return false;
  const stats = await Promise.all(saved.pods.map(p =>
    fetch('/api/v1/sessions/'+p).then(r => r.ok ? r.json() : null).catch(()=>null)));
  if(stats.some(s => !s || !s.ready)) return false;
  stats.forEach((s, i) => {
    const node = nodes[i];
    node.pod = s.pod; node.ip = s.ip;
    makeTerm(node);
    connect(node);
  });
  sessionUp(Math.min(...stats.map(s=>s.expires_at||Infinity)));
  return true;
}

function showExpiry(expiresAt){
  const el = $('#texpiry');
  if(!el || !expiresAt || expiresAt===Infinity){ if(el) el.textContent=''; return; }
  const mins = Math.max(0, Math.round((expiresAt*1000 - Date.now())/60000));
  el.textContent = 'expires in ~'+(mins>=60 ? Math.round(mins/60)+'h' : mins+'m');
}

function sessionUp(expiresAt){
  setStatus('connected', true);
  $('#boot').hidden = true;
  $('#stop').hidden = false;
  store.save(nodes.map(n=>n.pod));
  showExpiry(expiresAt);
  rewritePortLinks();
  // Keepalive: slide every Pod's idle window while the page is open AND
  // visible. A closed or long-hidden tab stops pinging, and the server's
  // idle GC reaps the Pods after --session-idle-ttl (default 10m) — that is
  // the whole abandoned-session story: no ping, no Pod.
  if(keepaliveTimer) clearInterval(keepaliveTimer);
  keepaliveTimer = setInterval(() => { if(!document.hidden) keepalive(); }, 60000);
}

async function keepalive(){
  let exp = null, gone = 0, live = 0;
  for(const node of nodes.filter(n=>n.pod && !n.stopped)){
    live++;
    try {
      const r = await fetch('/api/v1/sessions/'+node.pod+'/keepalive', {method:'POST'});
      if(r.ok) exp = (await r.json()).expires_at;
      else if(r.status===404){
        // Resetting the UI kills the session for the learner — double-check
        // the pod really is gone before believing it.
        const s = await fetch('/api/v1/sessions/'+node.pod);
        if(s.status===404) gone++;
      }
    } catch(e){}
  }
  if(live && gone === live){ expireUI(); return; }
  if(exp) showExpiry(exp);
}

// The session outlived the page's absence (idle GC): reset the console UI
// without issuing deletes for Pods that no longer exist.
function expireUI(){
  if(keepaliveTimer) clearInterval(keepaliveTimer);
  for(const node of nodes){
    if(node.ws){ node.stopped = true; try{ node.ws.close(); }catch(e){} }
    if(node.ro){ node.ro.disconnect(); node.ro=null; }
    if(node.term){ node.term.dispose(); node.term=null; node.fit=null; }
    node.pod=null; node.ip=null; node.ws=null; node.stopped=false; node.retries=0;
    nodeStatus(node, 'idle', false);
  }
  store.clear();
  setStatus('session expired — start a new one', false);
  showExpiry(null);
  $('#boot').hidden = false;
  $('#stop').hidden = true;
}

// Coming back to a hidden tab: ping immediately — either the Pods are still
// there (window slides, terminal resumes) or they were reaped (UI resets).
document.addEventListener('visibilitychange', () => {
  if(!document.hidden && nodes.some(n=>n.pod)) keepalive();
});

async function start(){
  setStatus('starting…', false);
  $('#boot').disabled = true;
  try {
    let exp = null;
    for(const node of nodes) exp = await bootNode(node);
    sessionUp(exp);
  } catch(e){
    setStatus('start failed', false);
  } finally {
    $('#boot').disabled = false;
  }
}

async function stop(){
  if(keepaliveTimer) clearInterval(keepaliveTimer);
  for(const node of nodes){
    node.stopped = true;
    if(node.ws) try{ node.ws.close(); }catch(e){}
    if(node.pod) try{ await fetch('/api/v1/sessions/'+node.pod, {method:'DELETE'}); }catch(e){}
    if(node.ro){ node.ro.disconnect(); node.ro=null; }
    if(node.term){ node.term.dispose(); node.term=null; node.fit=null; }
    node.pod=null; node.ip=null; node.stopped=false; node.retries=0;
    nodeStatus(node, 'idle', false);
  }
  store.clear();
  setStatus('idle', false);
  showExpiry(null);
  $('#boot').hidden = false;
  $('#stop').hidden = true;
}

// (Window-resize refitting is handled per-node by the ResizeObserver in
// makeTerm — the panel box resizes with the window, so no separate handler.)

// --- Click-to-run: .termN fenced blocks paste into terminal N. ---
function nodeFromTermRef(ref){ // ".term2" or "2" -> node
  const n = parseInt(String(ref||'').replace(/\D/g,''), 10) || 1;
  return nodes[n-1];
}
document.querySelectorAll('pre.term-code').forEach(pre => {
  pre.addEventListener('click', () => {
    const node = nodeFromTermRef(pre.dataset.term);
    if(!node || !node.ws || node.ws.readyState!==1){ setStatus('start a session first', false); return; }
    const code = pre.querySelector('code').textContent.replace(/\n?$/, '\n');
    node.ws.send(new TextEncoder().encode(code));
    node.term.focus();
  });
});

// --- Exposed-port links: [text](/){:data-term=".term2"}{:data-port="8080"}.
// Rewritten once a session is up, using the router host-encoding
// (ip<A-B-C-D>-<token>-<port>.<router host>) the in-cluster router decodes.
async function rewritePortLinks(){
  if(routerHost === null){
    try { routerHost = (await (await fetch('/api/v1/config')).json()).router_host || ''; }
    catch(e){ routerHost = ''; }
  }
  document.querySelectorAll('a[data-port]').forEach(a => {
    const node = nodeFromTermRef(a.dataset.term);
    if(!node || !node.ip) return;
    if(!routerHost){ a.title = 'exposed-port routing not configured (ROUTER_HOST)'; return; }
    // Stash the authored path once so re-running the rewrite (reattach,
    // second session) stays idempotent after href becomes a full URL.
    if(!a.dataset.path){
      const href = a.getAttribute('href') || '/';
      a.dataset.path = href.startsWith('/') ? href : '/';
    }
    const prefix = a.dataset.hostPrefix ? a.dataset.hostPrefix + '-' : '';
    const token = (node.pod||'').replace(/[^0-9a-z]/g, '') || 'i0';
    const host = prefix + 'ip' + node.ip.replace(/\./g,'-') + '-' + token + '-' + a.dataset.port + '.' + routerHost;
    const path = a.dataset.path;
    // {:data-protocol="https:"} overrides the scheme (legacy SDK contract);
    // default follows the page rather than the SDK's hardcoded http:.
    const proto = (a.dataset.protocol || location.protocol).replace(/:?$/, ':');
    let url = proto + '//' + host + path;
    if(a.dataset.hashCode){
      // The "Test Exercise" button (class exercise-demo, carrying the
      // challenge hash): the result page loads the verify script from here and
      // submits the screenshot proof against this hash (legacy contract). An
      // authored inline link carries no hash — it's a plain preview.
      url += (path.includes('?')?'&':'?') + 'hash_code=' + encodeURIComponent(a.dataset.hashCode) +
             '&lessonsDomain=' + encodeURIComponent(location.origin) +
             '&ctfdDomain=' + encodeURIComponent(location.origin);
    }
    a.href = url;
    a.target = '_blank';
    a.dataset.routed = '1';
  });
}

// --- Server-verified exercises: the button asks the platform to fetch the
// result page from the learner's own Pod and assert its content. Exact where
// the screenshot proof is only perceptual (a dHash can't see text), and it
// can't be produced by the browser — the page really has to serve it. ---
document.querySelectorAll('a.exercise-demo[data-verify]').forEach(a => {
  a.addEventListener('click', async (e) => {
    e.preventDefault(); // graded server-side; no need to open the page
    const v = a.parentElement.querySelector('.verdict');
    const node = nodeFromTermRef(a.dataset.term);
    if(!node || !node.pod){
      v.textContent = 'start a session first'; v.style.color='var(--muted)'; return;
    }
    v.textContent = 'checking your session…'; v.style.color='var(--muted)';
    try {
      const r = await fetch('/api/v1/challenges/verify', {method:'POST',
        headers:{'Content-Type':'application/json'},
        body: JSON.stringify({challenge_hash: a.dataset.hashCode, pod: node.pod})});
      const j = await r.json();
      const st = j && j.data && j.data.status;
      if(st === 'correct'){ v.textContent = '✓ verified — recorded'; v.style.color='var(--ok)'; }
      else if(st === 'unreachable'){ v.textContent = 'could not reach the page in your session yet'; v.style.color='var(--bad)'; }
      else if(st === 'incorrect'){ v.textContent = 'the page is not what the exercise expects yet'; v.style.color='var(--bad)'; }
      else { v.textContent = (j && j.error) || 'could not verify'; v.style.color='var(--bad)'; }
    } catch(e){ v.textContent='could not verify'; v.style.color='var(--bad)'; }
  });
});

// Before a session is up, port links point nowhere useful — swallow the
// click and hint instead (the legacy SDK bound window.open the same way).
document.querySelectorAll('a[data-port]').forEach(a => {
  a.addEventListener('click', (e) => {
    if(a.dataset.verify) return; // handled above (no navigation at all)
    if(a.dataset.routed) return;
    e.preventDefault();
    setStatus('start a session first', false);
  });
});

if(TERMS > 0){
  buildPanels();
  $('#boot').onclick = start;
  $('#stop').onclick = stop;
  reattach(store.load()).then(ok => { if(!ok) store.clear(); });
}

// --- Quiz: submit the selected choice's pre-salted hash. Grading happens
// server-side; the page only confirms the answer was *submitted* — it never
// reveals correct/incorrect (that would leak which choice is right, since
// each choice's hash is in the DOM). See the scoreboard for outcomes. ---
document.querySelectorAll('.quiz').forEach(q=>{
  const btn=q.querySelector('.quiz-submit'), v=q.querySelector('.verdict');
  btn.onclick=async()=>{
    const sel=q.querySelector('input[type=radio]:checked');
    if(!sel){ v.textContent='pick an answer'; v.style.color='var(--muted)'; return; }
    v.textContent='submitting…'; v.style.color='var(--muted)';
    try{
      const r=await fetch('/api/v1/challenges/attempt',{method:'POST',headers:{'Content-Type':'application/json'},
        body:JSON.stringify({challenge_hash:q.dataset.challenge, submission:sel.dataset.flag})});
      const j=await r.json();
      if(j&&j.success){ v.textContent='✓ answer submitted'; v.style.color='var(--ok)'; }
      else { v.textContent='could not submit'; v.style.color='var(--bad)'; }
    }catch(e){ v.textContent='could not submit'; v.style.color='var(--bad)'; }
  };
});
</script>
</body>
</html>`
