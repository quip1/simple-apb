// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Turning what the user dropped into uploadable files (brief §6.11, §10):
// - files (.m4b, .m4a, .mp3, .zip) go as they are;
// - a folder with .m4b/.m4a files sends those;
// - a folder of .mp3 files (with disc subfolders) becomes one .zip, stored
//   rather than compressed (mp3 doesn't shrink), with its cover image. The
//   server reads it as one book and orders the files naturally.

const AUDIO = /\.(m4b|m4a|mp3)$/i;
const SENDABLE = /\.(m4b|m4a|mp3|zip)$/i;
const COVER = /^(cover|folder|front)\.(jpe?g|png)$/i;

/** Files dropped on the window, waiting for the Uploads page to send them. */
export const pending: File[] = [];

interface Found {
  path: string;
  file: File;
}

function readEntries(dir: FileSystemDirectoryEntry): Promise<FileSystemEntry[]> {
  const reader = dir.createReader();
  const all: FileSystemEntry[] = [];
  return new Promise((resolve, reject) => {
    const next = () =>
      reader.readEntries((batch) => {
        if (!batch.length) resolve(all);
        else {
          all.push(...batch);
          next();
        }
      }, reject);
    next();
  });
}

function fileOf(e: FileSystemFileEntry): Promise<File> {
  return new Promise((resolve, reject) => e.file(resolve, reject));
}

async function walk(dir: FileSystemDirectoryEntry, prefix: string, depth: number): Promise<Found[]> {
  const out: Found[] = [];
  for (const e of await readEntries(dir)) {
    if (e.isFile) out.push({ path: prefix + e.name, file: await fileOf(e as FileSystemFileEntry) });
    else if (e.isDirectory && depth < 2) out.push(...(await walk(e as FileSystemDirectoryEntry, `${prefix}${e.name}/`, depth + 1)));
  }
  return out;
}

/** Reads a drop. Call it straight from the drop handler (entries expire after the event). */
export async function filesFromDrop(dt: DataTransfer): Promise<File[]> {
  const entries = Array.from(dt.items)
    .map((i) => i.webkitGetAsEntry?.())
    .filter((e): e is FileSystemEntry => !!e);
  const loose = entries.length ? [] : Array.from(dt.files);
  const out: File[] = loose.filter((f) => SENDABLE.test(f.name));
  for (const e of entries) {
    if (e.isFile) {
      const f = await fileOf(e as FileSystemFileEntry);
      if (SENDABLE.test(f.name)) out.push(f);
    } else if (e.isDirectory) {
      out.push(...(await fromFolder(e.name, await walk(e as FileSystemDirectoryEntry, "", 0))));
    }
  }
  return out;
}

async function fromFolder(name: string, found: Found[]): Promise<File[]> {
  const audio = found.filter((f) => AUDIO.test(f.path));
  const books = audio.filter((f) => /\.(m4b|m4a)$/i.test(f.path));
  if (books.length) return books.map((f) => f.file);
  if (!audio.length) return [];
  const cover = found.find((f) => !f.path.includes("/") && COVER.test(f.path));
  return [await storedZip(`${name}.zip`, cover ? [...audio, cover] : audio)];
}

// ── a minimal zip writer (STORED entries, UTF-8 names) ──

/** 1980-01-01, the earliest zip date (a zero date upsets some tools). */
const DOS_DATE = 0x21;

let crcTable: Uint32Array | null = null;

function crc32(crc: number, buf: Uint8Array): number {
  if (!crcTable) {
    crcTable = new Uint32Array(256);
    for (let n = 0; n < 256; n++) {
      let c = n;
      for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
      crcTable[n] = c >>> 0;
    }
  }
  let c = ~crc;
  for (let i = 0; i < buf.length; i++) c = crcTable[(c ^ buf[i]!) & 0xff]! ^ (c >>> 8);
  return ~c >>> 0;
}

async function crcOf(file: Blob): Promise<number> {
  let crc = 0;
  const step = 8 << 20;
  for (let at = 0; at < file.size; at += step) crc = crc32(crc, new Uint8Array(await file.slice(at, at + step).arrayBuffer()));
  return crc;
}

function header(size: number, fill: (v: DataView) => void): Uint8Array<ArrayBuffer> {
  const b = new Uint8Array(size);
  fill(new DataView(b.buffer));
  return b;
}

/** Zips the files without compression; fails above the 4 GB zip limit. */
export async function storedZip(name: string, files: Found[]): Promise<File> {
  const enc = new TextEncoder();
  const parts: BlobPart[] = [];
  const central: Uint8Array<ArrayBuffer>[] = [];
  let offset = 0;
  for (const f of files) {
    const nameBytes = enc.encode(f.path);
    const crc = await crcOf(f.file);
    const size = f.file.size;
    if (offset + size > 0xfffffff0) throw new Error("too_big");
    const local = header(30 + nameBytes.length, (v) => {
      v.setUint32(0, 0x04034b50, true);
      v.setUint16(4, 20, true);
      v.setUint16(6, 0x0800, true);
      v.setUint16(12, DOS_DATE, true);
      v.setUint32(14, crc, true);
      v.setUint32(18, size, true);
      v.setUint32(22, size, true);
      v.setUint16(26, nameBytes.length, true);
    });
    local.set(nameBytes, 30);
    central.push(
      (() => {
        const c = header(46 + nameBytes.length, (v) => {
          v.setUint32(0, 0x02014b50, true);
          v.setUint16(4, 20, true);
          v.setUint16(6, 20, true);
          v.setUint16(8, 0x0800, true);
          v.setUint16(14, DOS_DATE, true);
          v.setUint32(16, crc, true);
          v.setUint32(20, size, true);
          v.setUint32(24, size, true);
          v.setUint16(28, nameBytes.length, true);
          v.setUint32(42, offset, true);
        });
        c.set(nameBytes, 46);
        return c;
      })(),
    );
    parts.push(local, f.file);
    offset += local.length + size;
  }
  const cdSize = central.reduce((n, c) => n + c.length, 0);
  const end = header(22, (v) => {
    v.setUint32(0, 0x06054b50, true);
    v.setUint16(8, files.length, true);
    v.setUint16(10, files.length, true);
    v.setUint32(12, cdSize, true);
    v.setUint32(16, offset, true);
  });
  return new File([...parts, ...central, end], name, { type: "application/zip" });
}
