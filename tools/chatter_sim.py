import re,sys
lines=open(sys.argv[1]).read().split("\n")
i=lines.index("== Raw timelines of phases 3 and 4")
blk=lines[i+1:]; sep=blk.index("  ----")
def parse(b):
    ev=[]
    for l in b:
        m=re.match(r"\s+([\d.]+) ms\s+t=\d+\s+HOOK (L down|L up)",l)
        if m: ev.append((float(m.group(1)),m.group(2)))
    return ev
P3,P4=parse(blk[:sep]),parse(blk[sep+1:])
def sim(ev,T,H,G):
    # rule A: release shorter than T -> merge. rule B: previous segment held < H and release < G -> merge.
    out=0; down=None; lastup=None; lasthold=None; pending=False
    clicks=[]
    for t,w in ev:
        if w=="L down":
            if lastup is not None:
                g=t-lastup
                if g<T or (lasthold is not None and lasthold<H and g<G):
                    continue_merge=True
                else: continue_merge=False
            else: continue_merge=False
            if not continue_merge: out+=1; clicks.append(t)
            down=t
        else:
            lasthold=t-down; lastup=t
    return out,clicks
def groups(clicks,win=400):
    g=[];cur=[]
    for c in clicks:
        if cur and c-cur[-1]>win: g.append(len(cur));cur=[]
        cur.append(c)
    if cur:g.append(len(cur))
    return g
print("raw presses p3",sum(1 for e in P3 if e[1]=="L down"),"p4",sum(1 for e in P4 if e[1]=="L down"))
for T,H,G in [(0,0,0),(40,0,0),(45,0,0),(60,0,0),(85,0,0),(40,15,100),(45,15,100),(40,20,100),(45,20,100),(50,25,100)]:
    a,c3=sim(P3,T,H,G); b,c4=sim(P4,T,H,G)
    g3=groups(c3); g4=groups(c4)
    print(f"T={T:3} H={H:2} G={G:3} | p3 presses {a:2} groups {g3} | p4 presses {b:2} groups {g4}")
