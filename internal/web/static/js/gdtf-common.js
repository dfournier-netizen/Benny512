// gdtf-common.js — the words every GDTF library screen shares (G3b: Settings
// › GDTF Share, Fixture library › Add from GDTF library, the Device
// inspector's GDTF group and the device cards' tags). Pure formatting: it
// fetches nothing. The approved design is docs/design/gdtf-library/.
//
// Words carry every state; colour only reinforces it. In particular every
// file is labelled "Manufacturer file" or "User file" in words (GDTF Share's
// API terms, owner round 2 #2), never by a colour alone.
const GdtfCommon = (() => {
  const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const num = n => Number(n || 0).toLocaleString('en-US');

  // when: an API time in words ("Tue, 17 Nov 2026, 21:40", local time), or
  // '' for none. Go's zero time (0001-01-01) is "none", never a date.
  function when(iso) {
    if (!iso) return '';
    const d = new Date(iso);
    if (isNaN(d.getTime()) || d.getUTCFullYear() < 2000) return '';
    return d.toLocaleString('en-GB', { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' });
  }

  // day: a Unix-seconds date from GDTF Share's list ("3 Oct 2026"), '' for 0.
  function day(unix) {
    if (!unix) return '';
    return new Date(unix * 1000).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
  }

  function labelWords(l) {
    return l === 'Manufacturer' ? 'Manufacturer file' : l === 'User' ? 'User file' : 'Uploader unknown';
  }
  function labelTag(l) { return UI.tag(labelWords(l), l === 'Manufacturer' ? 'info' : undefined); }

  // mb: a file size in words; under 1 MB in KB, so a small file never
  // reads "0.0 MB" (which looks like an empty file).
  const mb = bytes => {
    const n = Number(bytes || 0);
    return n < 1048576 ? Math.max(1, Math.round(n / 1024)) + ' KB' : (n / 1048576).toFixed(1) + ' MB';
  };

  // modeText: "Mode 1 · 49 ch". A footprint GDTF Share did not give is said
  // so; it is never shown as 0.
  function modeText(m) {
    return `${m.name || 'Unnamed mode'} · ${m.dmxfootprint == null ? 'channels not listed' : m.dmxfootprint + ' ch'}`;
  }
  const modesText = modes => (modes || []).length ? modes.map(modeText).join('; ') : 'none listed';

  const hex = (v, width) => '0x' + Number(v).toString(16).toUpperCase().padStart(width, '0');

  return { esc, num, when, day, labelWords, labelTag, mb, modeText, modesText, hex };
})();
