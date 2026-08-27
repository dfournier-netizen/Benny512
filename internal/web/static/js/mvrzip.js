// mvrzip.js — generic, hand-rolled ZIP reader for Phase 2b MVR/GDTF patch
// import. No external libraries (no JSZip, no CDN) — this project hand-rolls
// its own protocol clients (see ws.js) and does the same here. The one
// "cheat" that's fine: DEFLATE decompression itself uses the browser's
// native DecompressionStream('deflate-raw') — that's a platform API, not a
// third-party dependency.
//
// This module knows nothing about MVR or GDTF. An .mvr file is a ZIP; each
// referenced fixture type is a <uuid>.gdtf entry inside it, which is ALSO
// just a ZIP (its own description.xml plus resources we don't parse here).
// openZip() is generic over any ArrayBuffer/Uint8Array so it works
// unmodified on the outer .mvr bytes and, recursively, on a .gdtf entry's
// bytes pulled out of that first pass — callers (the MVR/GDTF XML parser
// layer, built separately) just call openZip() again on whatever read()
// handed them.
//
// Lazy by design: only the central directory (filenames + metadata) is
// parsed up front. Actual decompression happens per-entry, only when
// read(name) is called — MVR files routinely carry large embedded
// thumbnails and 3D geometry that Benny512 has no use for and must never
// pay to inflate just because they're present in the archive.
//
// Correctness notes (the bugs a naive hand-rolled reader gets wrong):
//   - The End Of Central Directory record is found by scanning backward
//     from the end of the buffer for its signature, because it can be
//     preceded by a variable-length archive comment — never assume a fixed
//     offset from EOF.
//   - ZIP64 is explicitly unsupported: detecting the ZIP64 EOCD locator (or
//     any ZIP64 sentinel value in the plain EOCD's count/size/offset
//     fields) throws a clear error rather than silently misparsing.
//   - The actual compressed data for an entry does NOT start at
//     local-header-offset + 30. The local file header at that offset has
//     to be read itself — its filename-length and extra-field-length can
//     legitimately differ from the central directory's copies of those
//     same fields — and only ITS lengths tell you where the data begins.
//     Trusting the central directory's extra-field length here is the #1
//     way hand-rolled zip readers silently corrupt data on real files.
const MvrZip = (() => {
  const SIG_LOCAL_FILE_HEADER = 0x04034b50;
  const SIG_CENTRAL_DIR_HEADER = 0x02014b50;
  const SIG_EOCD = 0x06054b50;
  const SIG_ZIP64_EOCD_LOCATOR = 0x07064b50;

  const EOCD_MIN_SIZE = 22;
  const MAX_COMMENT_LEN = 0xffff;

  // CP437 codepoints for bytes 0x80-0xFF, in order (128 entries). Used to
  // decode filenames whose general-purpose bit 11 (UTF-8) is not set. Most
  // MVR/GDTF filenames are plain ASCII (bytes < 0x80 decode identically
  // either way) so this rarely matters in practice, but it's decoded
  // properly rather than assumed.
  const CP437_HIGH =
    'ÇüéâäàåçêëèïîìÄÅÉæÆôöòûùÿÖÜ¢£¥₧ƒáíóúñÑªº¿⌐¬½¼¡«»' +
    '░▒▓│┤╡╢╖╕╣║╗╝╜╛┐└┴┬├─┼╞╟╚╔╩╦╠═╬╧╨╤╥╙╘╒╓╫╪┘┌█▄▌▐▀' +
    'αßΓπΣσµτΦΘΩδ∞φε∩≡±≥≤⌠⌡÷≈°∙·√ⁿ²■ ';

  function decodeCp437(bytes) {
    let s = '';
    for (let i = 0; i < bytes.length; i++) {
      const b = bytes[i];
      s += b < 0x80 ? String.fromCharCode(b) : CP437_HIGH[b - 0x80];
    }
    return s;
  }

  // decodeName: general-purpose bit flag bit 11 (0x0800) means the
  // filename/comment fields are UTF-8; otherwise they're the legacy
  // (effectively CP437) encoding most zip tools default to.
  function decodeName(bytes, flag) {
    if (flag & 0x0800) return new TextDecoder('utf-8').decode(bytes);
    return decodeCp437(bytes);
  }

  // findEOCD: scan backward from EOF for the EOCD signature, since an
  // archive comment of up to 65535 bytes can sit between the record and
  // the true end of the buffer.
  function findEOCD(view, byteLength) {
    const searchFloor = Math.max(0, byteLength - EOCD_MIN_SIZE - MAX_COMMENT_LEN);
    for (let i = byteLength - EOCD_MIN_SIZE; i >= searchFloor; i--) {
      if (view.getUint32(i, true) === SIG_EOCD) return i;
    }
    throw new Error('not a valid zip: End Of Central Directory record not found');
  }

  function assertNotZip64(view, eocdOffset) {
    // The ZIP64 EOCD locator, when present, is a fixed 20 bytes
    // immediately before the (plain) EOCD record.
    if (eocdOffset >= 20 && view.getUint32(eocdOffset - 20, true) === SIG_ZIP64_EOCD_LOCATOR) {
      throw new Error('ZIP64 not supported (ZIP64 End Of Central Directory locator present)');
    }
    // Belt-and-braces: a plain EOCD record itself signals ZIP64 by filling
    // its 16/32-bit count/size/offset fields with all-1s sentinels when the
    // real values don't fit.
    const totalEntries = view.getUint16(eocdOffset + 10, true);
    const cdSize = view.getUint32(eocdOffset + 12, true);
    const cdOffset = view.getUint32(eocdOffset + 16, true);
    if (totalEntries === 0xffff || cdSize === 0xffffffff || cdOffset === 0xffffffff) {
      throw new Error('ZIP64 not supported (EOCD sentinel values present)');
    }
  }

  // readCentralDirectory: walk every central-directory file header, cheap
  // metadata only (name, method, sizes, local header offset) — no data is
  // touched or decompressed here.
  function readCentralDirectory(bytes, view, cdOffset, cdSize, entryCount) {
    const names = [];
    const entries = new Map();

    let p = cdOffset;
    const cdEnd = cdOffset + cdSize;
    for (let i = 0; i < entryCount; i++) {
      if (p + 46 > cdEnd || view.getUint32(p, true) !== SIG_CENTRAL_DIR_HEADER) {
        throw new Error(`bad zip: central directory file header signature mismatch at entry ${i}`);
      }
      const flag = view.getUint16(p + 8, true);
      const method = view.getUint16(p + 10, true);
      const compressedSize = view.getUint32(p + 20, true);
      const uncompressedSize = view.getUint32(p + 24, true);
      const fnLen = view.getUint16(p + 28, true);
      const extraLen = view.getUint16(p + 30, true);
      const commentLen = view.getUint16(p + 32, true);
      const localHeaderOffset = view.getUint32(p + 42, true);

      const nameBytes = bytes.subarray(p + 46, p + 46 + fnLen);
      const name = decodeName(nameBytes, flag);

      names.push(name);
      entries.set(name, { method, compressedSize, uncompressedSize, localHeaderOffset });

      p += 46 + fnLen + extraLen + commentLen;
    }

    return { names, entries };
  }

  // localDataStart: the #1 hand-rolled-zip-reader bug is trusting the
  // central directory's extra-field length to compute where an entry's
  // compressed data begins. The local file header at localHeaderOffset has
  // to be read for real — its own filename-length/extra-field-length
  // fields are the only ones that correctly describe the bytes that
  // actually precede the data at this offset in this file.
  function localDataStart(view, localHeaderOffset) {
    if (view.getUint32(localHeaderOffset, true) !== SIG_LOCAL_FILE_HEADER) {
      throw new Error(`bad zip: local file header signature mismatch at offset ${localHeaderOffset}`);
    }
    const fnLen = view.getUint16(localHeaderOffset + 26, true);
    const extraLen = view.getUint16(localHeaderOffset + 28, true);
    return localHeaderOffset + 30 + fnLen + extraLen;
  }

  function streamFromBytes(bytes) {
    return new ReadableStream({
      start(controller) {
        controller.enqueue(bytes);
        controller.close();
      },
    });
  }

  async function collectStream(stream) {
    const reader = stream.getReader();
    const chunks = [];
    let total = 0;
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      chunks.push(value);
      total += value.length;
    }
    const out = new Uint8Array(total);
    let pos = 0;
    for (const chunk of chunks) {
      out.set(chunk, pos);
      pos += chunk.length;
    }
    return out;
  }

  async function inflateRaw(compressedBytes) {
    const ds = new DecompressionStream('deflate-raw');
    const decompressed = streamFromBytes(compressedBytes).pipeThrough(ds);
    return collectStream(decompressed);
  }

  // openZip(arrayBufferOrUint8Array) -> { names, read(name) }
  //   names: string[] — every entry path in the archive's central
  //          directory, in the order they appear there.
  //   read(name): Promise<Uint8Array> — lazily locates the entry by exact
  //          name match, reads its real local file header to find the true
  //          data start, and returns the decompressed (or, for a stored
  //          entry, raw) bytes. Throws a clear Error if name isn't found,
  //          the entry's compression method isn't stored/deflate, or the
  //          zip couldn't be parsed at all (including ZIP64 archives).
  async function openZip(arrayBufferOrUint8Array) {
    const bytes =
      arrayBufferOrUint8Array instanceof Uint8Array
        ? arrayBufferOrUint8Array
        : new Uint8Array(arrayBufferOrUint8Array);
    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);

    const eocdOffset = findEOCD(view, bytes.byteLength);
    assertNotZip64(view, eocdOffset);

    const entryCount = view.getUint16(eocdOffset + 10, true);
    const cdSize = view.getUint32(eocdOffset + 12, true);
    const cdOffset = view.getUint32(eocdOffset + 16, true);

    const { names, entries } = readCentralDirectory(bytes, view, cdOffset, cdSize, entryCount);

    async function read(name) {
      const entry = entries.get(name);
      if (!entry) throw new Error(`entry not found in zip: ${name}`);

      const dataStart = localDataStart(view, entry.localHeaderOffset);
      const compressed = bytes.subarray(dataStart, dataStart + entry.compressedSize);

      if (entry.method === 0) return new Uint8Array(compressed); // stored: copy out, done
      if (entry.method === 8) return inflateRaw(compressed); // deflate
      throw new Error(`unsupported compression method ${entry.method} for entry: ${name}`);
    }

    return { names, read };
  }

  return { openZip };
})();
