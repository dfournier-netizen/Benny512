"""Offline E1.20 discovery arithmetic, not a simulation of an RS-485 waveform.

Encoding/checksum: ANSI E1.20-2025 Tables 7-1/7-2, local PDF pages 51-52.
The inputs below are the eight real-UID candidates on .90.1 from LOG29-34.
No packets are sent; no runtime configuration is read or changed.
"""
from functools import reduce
from itertools import combinations
from operator import and_, or_


def uid(suffix):
    return bytes.fromhex('4d500011' + suffix)


def mask(data):
    return bytes(v for b in data for v in (b | 0xaa, b | 0x55))


def encode(u):
    euid = mask(u)
    # Sum the twelve encoded UID bytes, NOT just the six decoded UID bytes.
    return euid + mask(sum(euid).to_bytes(2, 'big'))


def decode(encoded):
    recovered = bytes(encoded[i] & encoded[i+1] for i in range(0, 16, 2))
    checksum = int.from_bytes(recovered[6:], 'big')
    return recovered[:6], checksum, sum(encoded[:12]) == checksum


real = [uid(s) for s in ('58ea', '58fe', '58ff', '5907', '5918', '591c', '592d', '5940')]
extras = [uid(s) for s in ('593e', '597e', '59be', '59fe', '59bc', '59fc')]

print('Discovery checksums / encoded bodies (without preamble and separator):')
for u in [uid('58fe'), uid('58ff')] + extras:
    encoded = encode(u)
    assert decode(encoded) == (u, sum(mask(u)), True)
    assert sum(mask(u)) == 6*255 + sum(u)
    print(u.hex(), f'checksum={sum(mask(u)):04x}', encoded.hex())

print('\nExact aligned bitwise OR example:')
a, b = uid('58fe'), uid('5918')
combined = bytes(x | y for x, y in zip(encode(a), encode(b)))
recovered, checksum, valid = decode(combined)
assert recovered == uid('59fe') and valid and checksum == 0x07ff
assert combined == encode(uid('59fe'))
print(a.hex(), 'OR', b.hex(), '=>', recovered.hex(), f'checksum={checksum:04x}', 'valid=', valid)

print('\nExhaustive aligned OR/AND combinations of 2-8 real candidates:')
for extra in extras:
    matches = []
    for count in range(2, len(real)+1):
        for subset in combinations(real, count):
            for name, operation in [('OR', or_), ('AND', and_)]:
                merged = bytes(reduce(operation, values) for values in zip(*map(encode, subset)))
                result, checksum, valid = decode(merged)
                if result == extra:
                    matches.append((name, subset, valid))
    print(extra.hex(), 'UID matches=', len(matches), 'checksum-valid=', sum(m[2] for m in matches))
    for name, subset, valid in matches:
        if len(subset) == 2:
            print(' ', name, [u.hex() for u in subset], 'valid=', valid)

# Alter only the differing encoded UID bit; leave the suspect checksum intact.
altered = bytearray(encode(uid('58fe')))
altered[8] |= 1
assert decode(altered)[0] == uid('59fe')
assert not decode(altered)[2]
print('\nA lone 58FE -> 59FE UID-bit flip with the original checksum is INVALID.')
print('OR is a hypothetical received-bit pattern, not an asserted electrical law.')
print('Other ghost UIDs require more than this aligned OR/AND model.')
