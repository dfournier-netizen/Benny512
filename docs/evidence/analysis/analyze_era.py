"""Read-only raw-byte ERA audit. Optional arguments select capture numbers."""
from pathlib import Path
import re
import sys
from collections import Counter
from datetime import datetime

# Captures live in the sibling captures/ folder in the repo layout
# (docs/evidence/captures/), not beside this script as they did in Logs/.
ROOT = Path(__file__).resolve().parent.parent / 'captures'
# Explicit controls from LOG29-33, rather than treating every new UID as healthy.
HEALTHY = {'4d500011' + suffix for suffix in (
    '58ea', '58ff', '5907', '5918', '591c', '592d', '5940',
    '58f6', '5902', '5905', '590b', '5911', '592f', '5933', '5949')}
for number in (tuple(map(int, sys.argv[1:])) if sys.argv[1:] else (29, 30, 31, 32)):
    text = (ROOT / f'RDM-LOG{number}.txt').read_text()
    packets = []
    for m in re.finditer(r'^\[([^\]]+)\] (IN|OUT)\s+(\w+)\s+peer=(\S+).*?(?=^\[|\Z)', text, re.M | re.S):
        stamp, direction, kind, peer = m.groups()
        hx = re.search(r'hex: ([0-9a-f]+)', m.group())
        if not hx:
            continue
        b = bytes.fromhex(hx[1])
        p = dict(t=stamp, dt=datetime.fromisoformat(stamp), direction=direction, kind=kind,
                 peer=peer, line=text.count('\n', 0, m.start())+1, b=b)
        if kind == 'ArtRdm' and len(b) >= 49:
            p.update(dst=b[26:32].hex(), src=b[32:38].hex(), tn=b[38], pid=int.from_bytes(b[44:46], 'big'),
                     response=b[39], data=b[47:-2].hex(), checksum=(sum(b[24:-2])+0xcc)&0xffff == int.from_bytes(b[-2:], 'big'))
        packets.append(p)
    rdm = [p for p in packets if 'pid' in p]
    print(f'\nLOG{number}: {len(packets)} hex-bearing packet records; raw RDM checksum failures: {sum(not p["checksum"] for p in rdm)}')
    observed = {p[k] for p in rdm for k in ('src', 'dst') if p[k].startswith('4d50')}
    for p in packets:
        if p['kind'] == 'ArtTodData':
            observed.update(p['b'][i:i+6].hex() for i in range(28, len(p['b'])-5, 6)
                            if p['b'][i:i+2] == bytes.fromhex('4d50'))
    for uid in sorted(observed - HEALTHY):
        out = [p for p in rdm if p['direction']=='OUT' and p['dst']==uid]
        inc = [p for p in rdm if p['direction']=='IN' and p['src']==uid]
        print(uid, 'out/in',len(out),len(inc), 'PID requests',dict(Counter(f'{p["pid"]:04x}' for p in out)))
        for p in inc:
            prior = [q for q in out if q['tn']==p['tn'] and q['pid']==p['pid'] and q['dt']<=p['dt']]
            q = prior[-1] if prior else None
            print('  reply',p['t'],'line',p['line'],'PID',f'{p["pid"]:04x}','type',p['response'],'data',p['data'],
                  'valid',p['checksum'],'latest request delay ms',(p['dt']-q['dt']).total_seconds()*1000 if q else None)
    print('Martin tables (raw UID bytes):')
    for p in packets:
        if p['kind']=='ArtTodData':
            b=p['b']; uids=[b[i:i+6].hex() for i in range(28,len(b)-5,6)]
            if any(u.startswith('4d50') for u in uids):
                print(' ',p['t'],'line',p['line'],p['peer'],'raw address',b[23],uids)
    print('Healthy Martin response totals:')
    for peer in ['2.11.90.1:6454','2.11.90.6:6454']:
        out=[p for p in rdm if p['direction']=='OUT' and p['peer']==peer and p['dst'] in HEALTHY]
        inc=[p for p in rdm if p['direction']=='IN' and p['peer']==peer and p['src'] in HEALTHY]
        print(peer,len(out),len(inc))
