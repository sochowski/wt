#!/usr/bin/env python3
"""
wt penguin — reference animation generator.

This is the SOURCE OF TRUTH for the penguin mascot animations. It renders the
locked base sprite and its three animations (idle / waddle / wave) to GIFs, by
drawing Unicode block/quadrant glyphs as filled rectangles on a cell grid
(exactly how a terminal tiles them).

Run:  python3 penguin_gen.py        # writes idle.gif, waddle.gif, wave.gif
Deps: Pillow  (pip install pillow)

NOTE for the bash port: motion here uses sub-cell pixel offsets (body bob, foot
lift). A real terminal can only move things by WHOLE or HALF cells (half via
block glyphs), so the bash implementation must quantize these to cell steps.
"""
from PIL import Image, ImageDraw, ImageFont

# --- Unicode block-element -> filled quadrants (TL/TR/BL/BR) -----------------
Q={"█":"TL TR BL BR","▀":"TL TR","▄":"BL BR","▌":"TL BL","▐":"TR BR",
 "▖":"BL","▗":"BR","▘":"TL","▝":"TR","▙":"TL BL BR","▟":"TR BL BR",
 "▛":"TL TR BL","▜":"TL TR BR","▚":"TL BR","▞":"TR BL"}
CW,CH=26,38                       # cell size in px
BG=(24,24,37); W=(235,235,238)    # background / body color

# Any monospace TTF works — it's only used for the "z" sleep particles.
def _load_font(size):
    for p in ("/usr/share/fonts/Adwaita/AdwaitaMono-Regular.ttf",
              "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
              "/usr/share/fonts/gnu-free/FreeMono.otf",
              "/Library/Fonts/Menlo.ttc"):
        try: return ImageFont.truetype(p,size)
        except OSError: continue
    return ImageFont.load_default()

def cell(d,x,y,ch):
    """Draw one glyph at pixel (x,y). '▬' is a special drooped-eye bar."""
    if ch=="▬": d.rectangle([x+CW*0.12,y+CH*0.44,x+CW*0.88,y+CH*0.60],fill=W); return
    if ch not in Q: return
    x0,y0,x1,y1,xm,ym=x,y,x+CW,y+CH,x+CW/2,y+CH/2
    for q in Q[ch].split():
        rx0=x0 if q[1]=="L" else xm; rx1=xm if q[1]=="L" else x1
        ry0=y0 if q[0]=="T" else ym; ry1=ym if q[0]=="T" else y1
        d.rectangle([rx0,ry0,rx1-1,ry1-1],fill=W)

# =====================================================================
#  LOCKED BASE SPRITE
#     ▟ = left arm   ▌(c8) = right arm   ▙/▛ = back+tail   ▬/▘ = eyes
# =====================================================================
#  ▗▄▄▄▄▄▄▄▖
#  ▟▐ E  E ▌▌      (E = eye: ▬ sleepy / ▘ open)
#  ▐▄▄▄▄▄▄▄▛
#    ▝▘ ▝▘         (feet)

PAD=16
def new(cols,rows):
    img=Image.new("RGB",(cols*CW+PAD*2,rows*CH+PAD*2),BG); return img,ImageDraw.Draw(img)
def gif(name,imgs,durs,scale):
    big=[i.resize((int(i.width*scale),int(i.height*scale)),Image.NEAREST) for i in imgs]
    big[0].save(name,save_all=True,append_images=big[1:],duration=durs,loop=0,disposal=2)
    print("wrote",name)

# ---------------- IDLE ----------------  sleepy, body bobs, z z drift
def idle():
    BODY=[" ▗▄▄▄▄▄▄▄▖","▟▐ ▬  ▬ ▌▌"," ▐▄▄▄▄▄▄▄▛"]; FEET="   ▝▘ ▝▘"
    COLS,ROWS,TOP=15,6,2
    zf=_load_font(20); zf2=_load_font(26)
    def frame(bob,zs):
        img,d=new(COLS,ROWS)
        for c,ch in enumerate(FEET):
            if ch!=" ": cell(d,PAD+c*CW,PAD+(TOP+3)*CH,ch)          # feet planted
        for r,line in enumerate(BODY):
            for c,ch in enumerate(line):
                if ch!=" ": cell(d,PAD+c*CW,PAD+(TOP+r)*CH+bob,ch)  # body bobs
        for (zx,zy,big,fade) in zs:
            f=zf2 if big else zf; g=int(120+90*fade)
            d.text((PAD+zx*CW,PAD+zy*CH),"z",font=f,fill=(g,g,g+6))
        return img
    seq=[(0,[(9,2.0,0,0.2)]),(-4,[(9.3,1.4,0,0.6),(9,2.2,0,0.1)]),
     (-7,[(9.7,0.8,1,0.9),(9.3,1.6,0,0.4)]),(-7,[(10.2,0.2,1,0.5),(9.7,1.0,0,0.7)]),
     (-4,[(10.6,-0.3,1,0.2),(10.2,0.4,1,0.9)]),(0,[(10.6,-0.2,1,0.4)])]
    gif("idle.gif",[frame(b,z) for b,z in seq],[520]*6,1.6)

# ---------------- WADDLE ---------------- open eyes, skippy hop + legs
def waddle():
    BASE=[" ▗▄▄▄▄▄▄▄▖","▟▐ ▘  ▘ ▌▌"," ▐▄▄▄▄▄▄▄▛"]
    COLS,ROWS,TOP=14,8,3
    LF=[(3,"▝"),(4,"▘")]; RF=[(6,"▝"),(7,"▘")]
    def frame(bob,lift):
        img,d=new(COLS,ROWS)
        for c,ch in LF: cell(d,PAD+c*CW,PAD+(TOP+3)*CH-lift[0],ch)
        for c,ch in RF: cell(d,PAD+c*CW,PAD+(TOP+3)*CH-lift[1],ch)
        for r,line in enumerate(BASE):
            for c,ch in enumerate(line):
                if ch!=" ": cell(d,PAD+c*CW,PAD+(TOP+r)*CH+bob,ch)
        return img
    seq=[(0,(0,8)),(-12,(6,6)),(0,(8,0)),(-12,(6,6))]   # plant/hop/plant/hop
    gif("waddle.gif",[frame(b,l) for b,l in seq],[190,150,190,150],1.5)

# ---------------- WAVE ---------------- open eyes, one wing waggles (base in body)
def wave():
    BODY=[" ▗▄▄▄▄▄▄▄▖"," ▐ ▘  ▘ ▌▌"," ▐▄▄▄▄▄▄▄▛"]   # left arm removed; it's raised
    COLS,ROWS,TOP=14,8,3
    LF=[(3,"▝"),(4,"▘")]; RF=[(6,"▝"),(7,"▘")]
    def frame(hand):
        img,d=new(COLS,ROWS)
        for c,ch in LF: cell(d,PAD+c*CW,PAD+(TOP+3)*CH,ch)
        for c,ch in RF: cell(d,PAD+c*CW,PAD+(TOP+3)*CH,ch)
        for r,line in enumerate(BODY):
            for c,ch in enumerate(line):
                if ch!=" ": cell(d,PAD+c*CW,PAD+(TOP+r)*CH,ch)
        # raised left wing: base inside body (c1) + short tip that waggles
        cell(d,PAD+1*CW,PAD+int(round((TOP+0.05)*CH)),"▐")
        cell(d,PAD+1*CW,PAD+int(round((TOP-0.55)*CH)),hand)
        return img
    seq=["▙","▟","▙","▟"]
    gif("wave.gif",[frame(h) for h in seq],[220]*4,1.5)

if __name__=="__main__":
    idle(); waddle(); wave()
