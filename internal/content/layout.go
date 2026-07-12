package content

import (
	"fmt"
	"html"
)

// layout wraps rendered lesson HTML in a full page that is Play-With-Docker
// compatible in the way that matters: it embeds a live terminal panel and
// wires quizzes/exercises to the platform's own APIs — the equivalent of the
// legacy pwd.newSession + quiz.js + exercises.js glue, but talking to this
// binary's endpoints (/api/v1/sessions, /terminals/, /api/v1/challenges).
//
// The lesson's front-matter image boots the session instance; an empty image
// lets the server pick its default.
func layout(fm FrontMatter, body string) string {
	title := fm.Title
	if title == "" {
		title = "training lesson"
	}
	// Template order: title (%s), body (%s), image (%q as a JS string).
	return fmt.Sprintf(pageTmpl, html.EscapeString(title), body, fm.Image)
}

const pageTmpl = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
  :root { color-scheme: light dark; --bg:#0f1117; --panel:#171a23; --fg:#e6e8ee; --muted:#9aa3b2; --accent:#4f8cff; --ok:#31c48d; --bad:#f05252; --border:#262b38; }
  * { box-sizing:border-box; }
  body { margin:0; font:15px/1.6 -apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif; background:var(--bg); color:var(--fg); }
  header { padding:14px 24px; border-bottom:1px solid var(--border); display:flex; gap:14px; align-items:center; }
  header a { color:var(--accent); text-decoration:none; font-size:13px; }
  main { display:grid; grid-template-columns:1fr 1fr; gap:20px; padding:24px; max-width:1200px; margin:0 auto; }
  @media (max-width:900px){ main{ grid-template-columns:1fr; } }
  .col{ display:grid; gap:20px; align-content:start; }
  .card{ background:var(--panel); border:1px solid var(--border); border-radius:10px; overflow:hidden; }
  .card h2{ font-size:12px; text-transform:uppercase; letter-spacing:.08em; color:var(--muted); margin:0; padding:12px 16px; border-bottom:1px solid var(--border); }
  .card .body{ padding:16px; }
  article h1{ font-size:22px; margin:.1em 0 .4em; }
  article h2{ font-size:17px; margin:1em 0 .3em; }
  article code{ background:#0b0d13; padding:2px 6px; border-radius:4px; font-family:ui-monospace,Menlo,monospace; font-size:13px; }
  article pre{ background:#0b0d13; padding:12px; border-radius:6px; overflow:auto; }
  article pre code{ background:none; padding:0; }
  .term{ background:#0b0d13; color:#cfe3ff; font-family:ui-monospace,Menlo,monospace; font-size:13px; height:300px; padding:10px; overflow:auto; white-space:pre-wrap; word-break:break-all; }
  .quiz label,.exercise{ display:block; }
  .quiz label{ padding:8px 10px; border:1px solid var(--border); border-radius:6px; margin:6px 0; cursor:pointer; }
  .quiz label:hover{ border-color:var(--accent); }
  .quiz label.correct{ border-color:var(--ok); background:rgba(49,196,141,.1); }
  .quiz label.wrong{ border-color:var(--bad); background:rgba(240,82,82,.1); }
  .quiz .q,.exercise .q{ font-weight:600; margin-bottom:8px; }
  button,.exercise-demo{ background:var(--accent); color:#fff; border:0; border-radius:6px; padding:8px 14px; font-size:13px; cursor:pointer; text-decoration:none; display:inline-block; }
  .verdict{ margin-top:10px; font-weight:600; }
  .row{ display:flex; gap:8px; align-items:center; margin-bottom:8px; }
  .pill{ font-size:11px; color:var(--muted); }
</style>
</head>
<body>
<header><strong>training-platform</strong> <a href="/">lessons</a> <a href="/scoreboard">scoreboard</a></header>
<main>
  <div class="col">
    <section class="card"><h2>Lesson</h2><div class="body"><article>%s</article></div></section>
  </div>
  <div class="col">
    <section class="card"><h2>Console (play with docker)</h2><div class="body">
      <div class="row"><button id="boot">Start session</button><span class="pill" id="tstatus">idle</span></div>
      <div class="term term1" tabindex="0">Start a session to open a terminal…</div>
    </div></section>
  </div>
</main>
<script>
const lessonImage = %q;

// --- Console: boot a session Pod, then bridge a terminal over WebSocket. ---
let ws=null;
document.getElementById('boot').onclick = async () => {
  const stat=document.getElementById('tstatus'), termEl=document.querySelector('.term1');
  stat.textContent='starting…';
  let pod;
  try {
    const r=await fetch('/api/v1/sessions',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({image:lessonImage})});
    if(!r.ok){ stat.textContent='start failed ('+r.status+')'; return; }
    pod=(await r.json()).pod;
  } catch(e){ stat.textContent='start failed'; return; }
  const proto=location.protocol==='https:'?'wss':'ws';
  termEl.textContent='';
  ws=new WebSocket(proto+'://'+location.host+'/terminals/'+pod);
  ws.binaryType='arraybuffer';
  ws.onopen=()=>{ stat.textContent='connected: '+pod; stat.style.color='var(--ok)'; termEl.focus(); };
  ws.onclose=()=>{ stat.textContent='closed'; };
  ws.onmessage=(e)=>{ const d=(typeof e.data==='string')?e.data:new TextDecoder().decode(e.data); termEl.textContent+=d; termEl.scrollTop=termEl.scrollHeight; };
  termEl.onkeydown=(e)=>{
    if(!ws||ws.readyState!==1) return;
    let b; if(e.key==='Enter')b='\r'; else if(e.key==='Backspace')b='\x7f'; else if(e.key.length===1)b=e.key; else return;
    e.preventDefault(); ws.send(new TextEncoder().encode(b));
  };
};

// --- Quiz: submit the selected choice's pre-salted hash; graded server-side. ---
document.querySelectorAll('.quiz').forEach(q=>{
  const btn=q.querySelector('.quiz-submit'), v=q.querySelector('.verdict');
  btn.onclick=async()=>{
    const sel=q.querySelector('input[type=radio]:checked');
    if(!sel){ v.textContent='pick an answer'; return; }
    const r=await fetch('/api/v1/challenges/attempt',{method:'POST',headers:{'Content-Type':'application/json'},
      body:JSON.stringify({challenge_hash:q.dataset.challenge, submission:sel.dataset.flag})});
    const j=await r.json(); const st=(j.data&&j.data.status)||'error';
    q.querySelectorAll('label').forEach(l=>l.classList.remove('correct','wrong'));
    const lab=sel.closest('label');
    if(st==='correct'){ v.textContent='✓ correct'; v.style.color='var(--ok)'; lab.classList.add('correct'); }
    else if(st==='incorrect'){ v.textContent='✗ incorrect'; v.style.color='var(--bad)'; lab.classList.add('wrong'); }
    else { v.textContent='challenge not found'; }
  };
});

// --- Exercise: the "Test Exercise" link opens the result page carrying the
// hash_code the verify script submits its screenshot proof with. ---
document.querySelectorAll('.exercise-demo').forEach(a=>{
  a.addEventListener('click',(e)=>{
    e.preventDefault();
    const v=a.parentElement.querySelector('.verdict');
    v.textContent='opens the exercise result page with ?hash_code='+a.dataset.hashCode.slice(0,12)+'… (screenshot proof → phash grading)';
    v.style.color='var(--muted)';
  });
});
</script>
</body>
</html>`
