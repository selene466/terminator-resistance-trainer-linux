import struct,sys,zlib,os
def rd_str(b,o):
    n=struct.unpack_from('<i',b,o)[0];o+=4
    if n<0: n=-n; s=b[o:o+2*n-2].decode('utf-16le'); o+=2*n
    else: s=b[o:o+n-1].decode('latin1'); o+=n
    return s,o
def open_pak(p):
    f=open(p,'rb'); f.seek(0,2); sz=f.tell()
    f.seek(sz-240); t=f.read(240)
    i=t.find(struct.pack('<I',0x5A6F12E1)); ver=struct.unpack_from('<I',t,i+4)[0]
    off,isz=struct.unpack_from('<QQ',t,i+8)
    comp=[]
    if ver>=8:
        base=i+8+16+20+(1 if ver==9 else 0)
        for k in range(5):
            nm=t[base+32*k:base+32*k+32].split(b'\0')[0].decode(); comp.append(nm)
    f.seek(off); idx=f.read(isz)
    mp,o=rd_str(idx,0); cnt=struct.unpack_from('<i',idx,o)[0]; o+=4
    ents={}
    for _ in range(cnt):
        nm,o=rd_str(idx,o)
        eo,es,us,cm=struct.unpack_from('<QQQI',idx,o); o+=28
        o+=20
        blocks=[]
        if cm:
            nb=struct.unpack_from('<I',idx,o)[0]; o+=4
            for k in range(nb): blocks.append(struct.unpack_from('<QQ',idx,o)); o+=16
        o+=1; bs=struct.unpack_from('<I',idx,o)[0]; o+=4
        ents[mp+nm]=(eo,es,us,cm,blocks,bs)
    return f,ver,comp,ents
def read_entry(f,e,comp):
    eo,es,us,cm,blocks,bs=e
    hl=8*3+4+20+(4+16*len(blocks) if cm else 0)+5
    if not cm:
        f.seek(eo+hl); return f.read(us)
    out=b''
    for (s,en) in blocks:
        f.seek(eo+s); out+=zlib.decompress(f.read(en-s))
    return out[:us]

# Builds mods/easy_exp.pak from the game's own B_TerminatorGameState:
#  - per-level EXP array: 33 entries of EXP_PER_LEVEL (stock: 27 escalating entries)
#  - the 3 float groups (0.7,0.6,0.5 / 0.85,0.65,0.5) set to 1.0
# usage: python3 -I tools/mkexp.py <game Paks dir> [exp_per_level]
# writes internal/mod/exp.pak and the unpacked files under internal/mod/exp/
import hashlib

def build(paks, exp):
    out = "internal/mod/exp.pak"
    un = "internal/mod/exp/Terminator/Content/Blueprints/Abstract"
    f, v, c, e = open_pak(os.path.join(paks, "pakchunk0-WindowsNoEditor.pak"))
    base = "../../../Terminator/Content/Blueprints/Abstract/B_TerminatorGameState."
    ua = bytearray(read_entry(f, e[base + "uasset"], c))
    ux = bytearray(read_entry(f, e[base + "uexp"], c))
    hdr = struct.unpack_from("<i", ua, 24)[0]
    assert hdr == len(ua)

    # per-level array: name idx 0x7e, size 4+4*n, type 0x2e9, flag 0, count n
    head = bytes.fromhex("7e00000000000000") + struct.pack("<Q", 4 + 4 * 27) + bytes.fromhex("e902000000000000") + b"\x00" + struct.pack("<i", 27)
    assert ux.count(head) == 1
    p = ux.index(head)
    old = ux[p + len(head):p + len(head) + 27 * 4]
    assert struct.unpack("<27i", old)[:3] == (1000, 1000, 1000), "unexpected stock array"
    n = 33
    new = head.replace(struct.pack("<Q", 4 + 4 * 27), struct.pack("<Q", 4 + 4 * n)).replace(b"\x00" + struct.pack("<i", 27), b"\x00" + struct.pack("<i", n)) + struct.pack("<%di" % n, *([exp] * n))
    ux[p:p + len(head) + 27 * 4] = new
    grow = 4 * (n - 27)

    # float groups, only inside the 400 bytes before the array
    one = struct.pack("<f", 1.0)
    w = bytes(ux[p - 400:p])
    g1 = struct.pack("<fff", 0.7, 0.6, 0.5)
    g2 = struct.pack("<fff", 0.85, 0.65, 0.5)
    assert w.count(g1) == 2 and w.count(g2) == 1
    w = w.replace(g1, one * 3).replace(g2, one * 3)
    ux[p - 400:p] = w

    # export table: the export holding the change grows, later exports shift
    eo = struct.unpack_from("<i", ua, 24 + 4 + 4 + struct.unpack_from("<i", ua, 28)[0] + 4 * 6)[0]
    ec = struct.unpack_from("<i", ua, 24 + 4 + 4 + struct.unpack_from("<i", ua, 28)[0] + 4 * 5)[0]
    chg = hdr + p
    hit = 0
    for k in range(ec):
        o = eo + k * 104 + 28
        sz, off = struct.unpack_from("<QQ", ua, o)
        if off <= chg < off + sz:
            struct.pack_into("<Q", ua, o, sz + grow); hit += 1
        elif off > chg:
            struct.pack_into("<Q", ua, o + 8, off + grow)
    assert hit == 1
    bds = struct.unpack_from("<q", ua, 169)[0]
    struct.pack_into("<q", ua, 169, bds + grow)

    os.makedirs(un, exist_ok=True)
    open(un + "/B_TerminatorGameState.uasset", "wb").write(ua)
    open(un + "/B_TerminatorGameState.uexp", "wb").write(ux)

    # pak v3, uncompressed
    mp = "../../../Terminator/Content/Blueprints/Abstract/"
    body = b""; ents = []
    for name, data in (("B_TerminatorGameState.uasset", bytes(ua)), ("B_TerminatorGameState.uexp", bytes(ux))):
        h = hashlib.sha1(data).digest()
        rec = struct.pack("<QQI", len(data), len(data), 0) + h + b"\x00" + struct.pack("<I", 0)
        ents.append((name, len(body), rec))
        body += struct.pack("<Q", 0) + rec + data
    def fs(s): b = s.encode() + b"\0"; return struct.pack("<i", len(b)) + b
    idx = fs(mp) + struct.pack("<i", len(ents))
    for name, off, rec in ents:
        idx += fs(name) + struct.pack("<Q", off) + rec
    foot = struct.pack("<IIQQ", 0x5A6F12E1, 3, len(body), len(idx)) + hashlib.sha1(idx).digest()
    open(out, "wb").write(body + idx + foot)

if __name__ == "__main__":
    build(sys.argv[1], int(sys.argv[2]) if len(sys.argv) > 2 else 270)
