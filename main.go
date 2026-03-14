package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type Horse struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Color    string  `json:"color"`
	JColor   string  `json:"jcolor"`
	Number   int     `json:"number"`
	Position float64 `json:"position"`
	Speed    float64 `json:"speed"`
	Finished bool    `json:"finished"`
	Place    int     `json:"place"`
	FinishMs int64   `json:"finish_ms"`
}

type Race struct {
	ID        string   `json:"id"`
	Horses    []*Horse `json:"horses"`
	State     string   `json:"state"`
	StartedAt int64    `json:"started_at"`
	mu        sync.Mutex
	clients   map[chan string]bool
	clientMu  sync.Mutex
}

type RaceStore struct {
	races map[string]*Race
	mu    sync.Mutex
}

var store = &RaceStore{races: make(map[string]*Race)}

var horseColors = []string{"#e94560", "#0cbc8b", "#f5a623", "#7c3aed", "#0ea5e9", "#ec4899", "#84cc16", "#f97316"}
var jockeyColors = []string{"#c0392b", "#0369a1", "#854F0B", "#4c1d95", "#0c4a6e", "#9d174d", "#3f6212", "#9a3412"}

func randomID() string {
	return fmt.Sprintf("%04d", rand.Intn(9000)+1000)
}

func (r *Race) broadcast(msg string) {
	r.clientMu.Lock()
	defer r.clientMu.Unlock()
	for ch := range r.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}
func (r *Race) addClient(ch chan string) {
	r.clientMu.Lock()
	defer r.clientMu.Unlock()
	r.clients[ch] = true
}
func (r *Race) removeClient(ch chan string) {
	r.clientMu.Lock()
	defer r.clientMu.Unlock()
	delete(r.clients, ch)
	close(ch)
}

func (r *Race) run() {
	r.mu.Lock()
	r.State = "racing"
	r.StartedAt = time.Now().UnixMilli()
	for _, h := range r.Horses {
		h.Speed = rand.Float64()*0.8 + 0.6
	}
	r.mu.Unlock()

	finishOrder := 0
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now().UnixMilli()
		r.mu.Lock()
		allDone := true
		for _, h := range r.Horses {
			if !h.Finished {
				allDone = false
				move := (0.4 + h.Speed*0.3) + (rand.Float64()*0.4 - 0.2)
				h.Position += move
				if h.Position >= 100 {
					h.Position = 100
					h.Finished = true
					finishOrder++
					h.Place = finishOrder
					h.FinishMs = now - r.StartedAt
				}
			}
		}
		r.mu.Unlock()

		r.mu.Lock()
		msg := marshalRace(r)
		r.mu.Unlock()
		r.broadcast("data: " + msg + "\n\n")

		if allDone {
			r.mu.Lock()
			r.State = "finished"
			r.mu.Unlock()
			r.broadcast("data: " + marshalRace(r) + "\n\n")
			return
		}
	}
}

func marshalRace(r *Race) string {
	horses := ""
	for i, h := range r.Horses {
		if i > 0 {
			horses += ","
		}
		horses += fmt.Sprintf(
			`{"id":%q,"name":%q,"color":%q,"jcolor":%q,"number":%d,"position":%.2f,"speed":%.2f,"finished":%v,"place":%d,"finish_ms":%d}`,
			h.ID, h.Name, h.Color, h.JColor, h.Number, h.Position, h.Speed, h.Finished, h.Place, h.FinishMs,
		)
	}
	return fmt.Sprintf(`{"id":%q,"state":%q,"started_at":%d,"horses":[%s]}`, r.ID, r.State, r.StartedAt, horses)
}

func createRace(c *gin.Context) {
	id := randomID()
	race := &Race{
		ID:      id,
		State:   "waiting",
		Horses:  []*Horse{},
		clients: make(map[chan string]bool),
	}
	store.mu.Lock()
	store.races[id] = race
	store.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{"id": id})
}

func getRace(c *gin.Context) {
	id := c.Param("id")
	store.mu.Lock()
	race, ok := store.races[id]
	store.mu.Unlock()
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "race not found"})
		return
	}
	race.mu.Lock()
	defer race.mu.Unlock()
	c.JSON(http.StatusOK, race)
}

func addHorse(c *gin.Context) {
	id := c.Param("id")
	store.mu.Lock()
	race, ok := store.races[id]
	store.mu.Unlock()
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "race not found"})
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := c.BindJSON(&body); err != nil || body.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name required"})
		return
	}
	race.mu.Lock()
	if race.State != "waiting" {
		race.mu.Unlock()
		c.JSON(http.StatusBadRequest, gin.H{"error": "race already started"})
		return
	}
	idx := len(race.Horses)
	horse := &Horse{
		ID:     randomID(),
		Name:   body.Name,
		Color:  horseColors[idx%len(horseColors)],
		JColor: jockeyColors[idx%len(jockeyColors)],
		Number: idx + 1,
	}
	race.Horses = append(race.Horses, horse)
	race.mu.Unlock()

	race.mu.Lock()
	msg := "data: " + marshalRace(race) + "\n\n"
	race.mu.Unlock()
	race.broadcast(msg)

	c.JSON(http.StatusOK, horse)
}

func startRace(c *gin.Context) {
	id := c.Param("id")
	store.mu.Lock()
	race, ok := store.races[id]
	store.mu.Unlock()
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "race not found"})
		return
	}
	race.mu.Lock()
	if race.State != "waiting" {
		race.mu.Unlock()
		c.JSON(http.StatusBadRequest, gin.H{"error": "race already started or finished"})
		return
	}
	if len(race.Horses) < 2 {
		race.mu.Unlock()
		c.JSON(http.StatusBadRequest, gin.H{"error": "need at least 2 horses"})
		return
	}
	race.mu.Unlock()
	go race.run()
	c.JSON(http.StatusOK, gin.H{"status": "started"})
}

func resetRace(c *gin.Context) {
	id := c.Param("id")
	store.mu.Lock()
	race, ok := store.races[id]
	store.mu.Unlock()
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "race not found"})
		return
	}
	race.mu.Lock()
	for _, h := range race.Horses {
		h.Position = 0
		h.Finished = false
		h.Place = 0
		h.FinishMs = 0
		h.Speed = 0
	}
	race.State = "waiting"
	race.StartedAt = 0
	msg := "data: " + marshalRace(race) + "\n\n"
	race.mu.Unlock()
	race.broadcast(msg)
	c.JSON(http.StatusOK, gin.H{"status": "reset"})
}

func sseStream(c *gin.Context) {
	id := c.Param("id")
	store.mu.Lock()
	race, ok := store.races[id]
	store.mu.Unlock()
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "race not found"})
		return
	}
	ch := make(chan string, 128)
	race.addClient(ch)
	defer race.removeClient(ch)

	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	race.mu.Lock()
	initial := marshalRace(race)
	race.mu.Unlock()
	fmt.Fprintf(w, "data: %s\n\n", initial)
	flusher.Flush()

	ctx := c.Request.Context()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprint(w, msg)
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<meta name="theme-color" content="#0a0a14">
<title>Better Horse Race</title>
<link rel="manifest" href="/manifest.json">
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{background:#0a0a14;color:#eee;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;min-height:100vh;padding:0 0 4rem}
.header{background:#0d1117;border-bottom:2px solid #e94560;padding:.9rem 1.5rem;display:flex;align-items:center;gap:.75rem}
.header h1{font-size:19px;font-weight:700;color:#fff;letter-spacing:-.01em}
.container{max-width:680px;margin:0 auto;padding:1.25rem 1rem}
.card{background:#0d1117;border:1px solid #1a2540;border-radius:12px;padding:1.2rem;margin-bottom:.9rem}
.btn{border:none;border-radius:8px;padding:.6rem 1.2rem;font-size:14px;font-weight:600;cursor:pointer}
.btn:disabled{opacity:.35;cursor:not-allowed}
.btn-red{background:#e94560;color:#fff}
.btn-green{background:#0cbc8b;color:#fff}
.btn-ghost{background:#141e30;color:#94a3b8;border:1px solid #1a2540}
.btn-sm{padding:.35rem .8rem;font-size:13px}
.input{background:#080d16;border:1px solid #1a2540;border-radius:8px;color:#fff;font-size:15px;padding:.55rem .9rem;width:100%}
.input::placeholder{color:#334}
.sec{font-size:10px;font-weight:600;color:#3d5070;text-transform:uppercase;letter-spacing:.12em;margin-bottom:.65rem}
.horse-row{display:flex;align-items:center;gap:.6rem;background:#080d16;border-radius:7px;padding:.45rem .8rem;margin-bottom:.3rem}
.num-badge{width:20px;height:20px;border-radius:4px;display:flex;align-items:center;justify-content:center;font-size:11px;font-weight:700;flex-shrink:0}
.dot{width:10px;height:10px;border-radius:50%;flex-shrink:0}
.status{display:inline-block;padding:2px 9px;border-radius:4px;font-size:11px;font-weight:600}
.s-wait{background:#111a27;color:#3d5070}
.s-race{background:#e94560;color:#fff;animation:pulse .9s infinite}
.s-fin{background:#0cbc8b;color:#fff}
@keyframes pulse{0%,100%{opacity:1}50%{opacity:.5}}
.add-row{display:flex;gap:.5rem;margin-bottom:.9rem}
.home-view,.race-view{display:none}
.home-view.active,.race-view.active{display:block}
canvas{display:block;width:100%;border-radius:8px;background:#070b12}
.res-row{display:flex;align-items:center;gap:.7rem;background:#080d16;border-radius:7px;padding:.55rem .9rem;margin-bottom:.3rem}
.pb{width:24px;height:24px;border-radius:50%;display:flex;align-items:center;justify-content:center;font-weight:700;font-size:11px;flex-shrink:0}
.p1{background:#f5a623;color:#000}.p2{background:#94a3b8;color:#000}.p3{background:#cd7f32;color:#fff}.px{background:#141e30;color:#556}
.ft{margin-left:auto;font-size:12px;color:#3d5070;font-family:monospace}
</style>
</head>
<body>
<div class="header">
  <svg width="20" height="20" viewBox="0 0 20 20"><text y="16" font-size="16">🏇</text></svg>
  <h1>Better Horse Race</h1>
</div>
<div class="container">

<div class="home-view active" id="hv">
  <div class="card">
    <div class="sec">new race</div>
    <button class="btn btn-red" onclick="newRace()" style="width:100%;margin-bottom:1rem">Create Race</button>
    <div class="sec">join race</div>
    <div class="add-row">
      <input class="input" id="join-id" placeholder="4-digit race code" maxlength="4">
      <button class="btn btn-ghost" onclick="joinRace()">Join</button>
    </div>
  </div>
</div>

<div class="race-view" id="rv">
  <div class="card">
    <div style="display:flex;align-items:center;justify-content:space-between">
      <div>
        <div class="sec">race code</div>
        <div style="font-size:34px;font-weight:800;letter-spacing:.1em;color:#fff;line-height:1" id="rc"></div>
      </div>
      <span class="status s-wait" id="rs">Waiting</span>
    </div>
  </div>

  <div class="card" id="sc">
    <div class="sec">add contestants</div>
    <div class="add-row">
      <input class="input" id="hn" placeholder="Name anything — person, beer, team..." maxlength="24">
      <button class="btn btn-ghost btn-sm" onclick="addHorse()">Add</button>
    </div>
    <div id="hl"></div>
    <button class="btn btn-green" id="sb" onclick="startRace()" disabled style="width:100%;margin-top:.5rem">Start Race</button>
  </div>

  <div class="card" id="tc" style="display:none">
    <div class="sec">live race</div>
    <canvas id="cv"></canvas>
  </div>

  <div class="card" id="resc" style="display:none">
    <div class="sec">results</div>
    <div id="res"></div>
    <button class="btn btn-ghost" onclick="resetRace()" style="width:100%;margin-top:.9rem">Race Again</button>
  </div>

  <button class="btn btn-ghost btn-sm" onclick="goHome()" style="margin-top:.4rem">← Back</button>
</div>
</div>

<script>
let raceId = null, es = null, last = null, raf = null;

const $ = id => document.getElementById(id);

function showHome(){ $('hv').classList.add('active'); $('rv').classList.remove('active'); }
function showRace(){ $('hv').classList.remove('active'); $('rv').classList.add('active'); }
function goHome(){ if(es){es.close();es=null;} if(raf){cancelAnimationFrame(raf);raf=null;} raceId=null; last=null; showHome(); }

async function newRace(){
  const r = await fetch('/api/races',{method:'POST'});
  const d = await r.json();
  loadRace(d.id);
}
function joinRace(){ const id=$('join-id').value.trim(); if(id) loadRace(id); }

function loadRace(id){
  raceId=id; last=null;
  $('rc').textContent=id;
  $('hl').innerHTML='';
  $('sb').disabled=true;
  $('sc').style.display='block';
  $('tc').style.display='none';
  $('resc').style.display='none';
  showRace();
  connectSSE(id);
}

function connectSSE(id){
  if(es) es.close();
  es=new EventSource('/api/races/'+id+'/stream');
  es.onmessage=e=>update(JSON.parse(e.data));
  es.onerror=()=>setTimeout(()=>connectSSE(id),2000);
}

function update(race){
  last=race;
  const s=$('rs');
  s.textContent=race.state[0].toUpperCase()+race.state.slice(1);
  s.className='status s-'+{waiting:'wait',racing:'race',finished:'fin'}[race.state];

  if(race.state==='waiting'){
    if(raf){cancelAnimationFrame(raf);raf=null;}
    renderList(race.horses);
    $('sb').disabled=race.horses.length<2;
    $('sc').style.display='block';
    $('tc').style.display='none';
    $('resc').style.display='none';
  } else if(race.state==='racing'){
    $('sc').style.display='none';
    $('tc').style.display='block';
    $('resc').style.display='none';
    startAnim();
  } else {
    if(raf){cancelAnimationFrame(raf);raf=null;}
    $('sc').style.display='none';
    $('tc').style.display='block';
    $('resc').style.display='block';
    draw(race.horses);
    renderResults(race.horses);
  }
}

function renderList(horses){
  $('hl').innerHTML=horses.map(h=>
    '<div class="horse-row">'+
    '<div class="num-badge" style="background:'+h.color+';color:#000">'+h.number+'</div>'+
    '<div class="dot" style="background:'+h.jcolor+'"></div>'+
    '<span style="font-size:13px;font-weight:500">'+esc(h.name)+'</span>'+
    '</div>'
  ).join('');
}

function startAnim(){
  function frame(){
    if(last && last.state==='racing'){
      draw(last.horses);
      raf=requestAnimationFrame(frame);
    }
  }
  if(raf) cancelAnimationFrame(raf);
  raf=requestAnimationFrame(frame);
}

// ── Canvas drawing ────────────────────────────────────────────────────────────
function draw(horses){
  const cv=$('cv');
  const laneH=58;
  const padT=24, padB=28;
  const H=horses.length*laneH+padT+padB;
  cv.height=H;
  cv.width=cv.offsetWidth||600;
  const W=cv.width;
  const ctx=cv.getContext('2d');
  const trackL=12;
  const trackR=W-24;
  const trackW=trackR-trackL;

  ctx.clearRect(0,0,W,H);
  ctx.fillStyle='#070b12';
  ctx.fillRect(0,0,W,H);

  // Grass strips
  ctx.fillStyle='#091209';
  ctx.fillRect(trackL,padT,trackW,10);
  ctx.fillStyle='#091209';
  ctx.fillRect(trackL,H-padB-10,trackW,10);

  // Dirt track
  ctx.fillStyle='#110d08';
  ctx.fillRect(trackL,padT+10,trackW,H-padT-padB-20);

  // Fence
  ctx.strokeStyle='#3d2808';
  ctx.lineWidth=2;
  ctx.beginPath();
  ctx.moveTo(trackL,H-padB);
  ctx.lineTo(trackR,H-padB);
  ctx.stroke();
  for(let fx=trackL;fx<=trackR;fx+=50){
    ctx.beginPath();
    ctx.moveTo(fx,H-padB-5);
    ctx.lineTo(fx,H-padB+7);
    ctx.stroke();
  }

  // Finish line
  ctx.strokeStyle='rgba(255,255,255,0.4)';
  ctx.lineWidth=2;
  ctx.setLineDash([4,4]);
  ctx.beginPath();
  ctx.moveTo(trackR,padT);
  ctx.lineTo(trackR,H-padB);
  ctx.stroke();
  ctx.setLineDash([]);
  ctx.fillStyle='rgba(255,255,255,0.25)';
  ctx.font='9px sans-serif';
  ctx.textAlign='center';
  ctx.fillText('FINISH',trackR,padT-6);

  // Lane dividers
  horses.forEach((_,i)=>{
    if(i===0) return;
    const y=padT+10+i*laneH-(laneH*0.08);
    ctx.strokeStyle='rgba(255,255,255,0.04)';
    ctx.lineWidth=1;
    ctx.beginPath();
    ctx.moveTo(trackL,y);
    ctx.lineTo(trackR,y);
    ctx.stroke();
  });

  const t=Date.now();
  horses.forEach((h,i)=>{
    const centerY=padT+10+(i+0.5)*laneH+4;
    const pct=Math.min(h.position,100)/100;
    const hx=trackL+pct*trackW;

    // Glow trail
    if(h.position>2){
      const grad=ctx.createLinearGradient(Math.max(trackL,hx-70),0,hx-4,0);
      grad.addColorStop(0,'rgba(0,0,0,0)');
      grad.addColorStop(1,h.color+'55');
      ctx.fillStyle=grad;
      ctx.fillRect(Math.max(trackL,hx-70),centerY-7,Math.min(70,hx-trackL),14);
    }

    // Floating name above horse
    const nameX=Math.min(hx, trackR-20);
    ctx.font='500 11px -apple-system,sans-serif';
    ctx.fillStyle=h.color;
    ctx.textAlign='center';
    ctx.textBaseline='bottom';
    const short=h.name.length>10?h.name.slice(0,9)+'…':h.name;
    ctx.fillText(short, nameX, centerY-14);

    // Draw horse (smaller S=1.5)
    drawPixelHorse(ctx,hx,centerY,h.color,h.jcolor,h.number,h.finished,t);

    // Place badge
    if(h.finished){
      const bc=['#f5a623','#94a3b8','#cd7f32'];
      ctx.fillStyle=bc[h.place-1]||'#141e30';
      ctx.beginPath();
      ctx.arc(trackR+14,centerY,9,0,Math.PI*2);
      ctx.fill();
      ctx.fillStyle=h.place<=2?'#000':'#fff';
      ctx.font='bold 9px sans-serif';
      ctx.textAlign='center';
      ctx.textBaseline='middle';
      ctx.fillText(h.place,trackR+14,centerY);
    }
  });
}

function drawPixelHorse(ctx,x,y,hc,jc,num,finished,t){
  const S=1.5; // pixel size
  const p=(px,py,w,h,c)=>{
    ctx.fillStyle=c;
    ctx.fillRect(x+px*S,y+py*S,w*S,h*S);
  };

  // Gallop phase
  const phase=finished?0:Math.sin(t/90)*1;
  const p2=finished?0:Math.sin(t/90+1.5)*1;

  // Horse body - sleek horizontal
  p(-11,0,5,3,hc);   // rump
  p(-6,-1,8,4,hc);   // mid body
  p(2,-1,4,5,hc);    // front shoulder
  // Neck
  p(6,-4,3,5,hc);
  // Head
  p(8,-6,6,5,hc);
  // Nose/muzzle
  p(12,-4,4,2,hc);
  // Ear
  p(9,-8,2,3,hc);
  // Eye
  p(13,-5,1,1,'#fff');
  // Tail
  p(-12,-1,2,3,hc);
  p(-14,0,2,4,hc);
  p(-13,3,3,2,hc);

  // Legs with gallop
  const fl1y = Math.round(p2);
  const fl2y = Math.round(-p2);
  const bl1y = Math.round(-phase);
  const bl2y = Math.round(phase);

  // Front legs
  p(4,4+fl1y,2,5,hc);
  p(2,4+fl2y,2,5,hc);
  // Back legs
  p(-8,4+bl1y,2,5,hc);
  p(-10,4+bl2y,2,5,hc);

  // Jockey body (sits on top of shoulder area)
  p(0,-9,8,7,jc);
  // Jockey arms
  p(7,-7,4,2,jc);
  // Jockey jersey number (white square on back)
  p(1,-8,5,5,'#fff');
  // Number text
  ctx.fillStyle=jc;
  ctx.font='bold '+(S*3.2)+'px sans-serif';
  ctx.textAlign='center';
  ctx.textBaseline='middle';
  ctx.fillText(num, x+(3.5*S), y+(-5.5*S));
  // Jockey head
  p(1,-13,5,4,'#f5c8a0');
  // Helmet
  p(0,-16,7,4,jc);
  p(0,-17,7,2,jc);
  // Helmet brim
  p(-1,-14,8,2,jc);
  // Riding crop (arm extended)
  p(10,-7,1,4,'#8b6914');
}

function renderResults(horses){
  const sorted=[...horses].sort((a,b)=>a.place-b.place);
  $('res').innerHTML=sorted.map(h=>{
    const cls=h.place<=3?'p'+h.place:'px';
    const secs=h.finish_ms>0?(h.finish_ms/1000).toFixed(2)+'s':'—';
    return '<div class="res-row">'+
      '<div class="pb '+cls+'">'+h.place+'</div>'+
      '<div style="width:10px;height:10px;border-radius:3px;background:'+h.color+';flex-shrink:0"></div>'+
      '<span style="font-size:13px;font-weight:500;flex:1">'+esc(h.name)+'</span>'+
      '<span class="ft">'+secs+'</span>'+
      '</div>';
  }).join('');
}

async function addHorse(){
  const inp=$('hn');
  const name=inp.value.trim();
  if(!name||!raceId) return;
  await fetch('/api/races/'+raceId+'/horses',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name})});
  inp.value=''; inp.focus();
}
async function startRace(){ if(raceId) await fetch('/api/races/'+raceId+'/start',{method:'POST'}); }
async function resetRace(){ if(raceId) await fetch('/api/races/'+raceId+'/reset',{method:'POST'}); }
function esc(s){ return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;'); }

$('hn').addEventListener('keydown',e=>{ if(e.key==='Enter') addHorse(); });
$('join-id').addEventListener('keydown',e=>{ if(e.key==='Enter') joinRace(); });
</script>
</body>
</html>`

const manifestJSON = `{
  "name": "Better Horse Race",
  "short_name": "Horse Race",
  "start_url": "/",
  "display": "standalone",
  "background_color": "#0a0a14",
  "theme_color": "#0a0a14",
  "description": "Race anything against anything",
  "icons": [{"src":"/icon.png","sizes":"192x192","type":"image/png"}]
}`

func main() {
	rand.Seed(time.Now().UnixNano())
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	r.GET("/", func(c *gin.Context) {
		c.Header("Content-Type", "text/html")
		c.String(http.StatusOK, indexHTML)
	})
	r.GET("/manifest.json", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.String(http.StatusOK, manifestJSON)
	})

	api := r.Group("/api")
	api.POST("/races", createRace)
	api.GET("/races/:id", getRace)
	api.POST("/races/:id/horses", addHorse)
	api.POST("/races/:id/start", startRace)
	api.POST("/races/:id/reset", resetRace)
	api.GET("/races/:id/stream", sseStream)

	fmt.Println("🏇 Better Horse Race running on :8765")
	r.Run(":8765")
}
