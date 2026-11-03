/* eslint-disable @cspell/spellchecker */
/**
 * Scrubber for Hive's local crash records and diagnostics reports.
 *
 * Everything Hive writes into a crash record or a saved diagnostics report
 * passes through here first. From plain text and from JSON-like values it
 * removes:
 *  - recovery phrases: 12 or more consecutive BIP39 English words;
 *  - JWTs, `Bearer` / `Basic` credentials, and the values of fields whose
 *    names say they hold a secret (password, passphrase, secret, token, key,
 *    mnemonic, seed, ...), in JSON, in `util.inspect` output and in key=value
 *    form;
 *  - private keys and other key material: hex or base64/base58 runs of 64 or
 *    more characters, lists of 16 or more byte values, and Buffer dumps;
 *  - email addresses;
 *  - the user's home folder and other user-profile folders (shown as `~`).
 *
 * The rules are deliberately greedy: over-redacting a log line costs a little
 * diagnostic detail, while a missed secret could cost someone their wallet.
 * Pure functions, no I/O.
 */
import os from 'os';

import { wordlists } from 'bip39';

export const REDACTED = '<redacted>';
export const REDACTED_RECOVERY_PHRASE = '<redacted-recovery-phrase>';
export const REDACTED_JWT = '<redacted-jwt>';
export const REDACTED_EMAIL = '<redacted-email>';
export const redactedHex = (length: number) => `<redacted-hex-${length}>`;
export const redactedBase64 = (length: number) => `<redacted-base64-${length}>`;
export const redactedBytes = (count: number) => `<redacted-bytes-${count}>`;

/** Fewest consecutive wordlist words that count as a recovery phrase. */
export const RECOVERY_PHRASE_MIN_WORDS = 12;
/** Shortest hex or base64 run that counts as key material. */
export const KEY_MATERIAL_MIN_LENGTH = 64;
/** Fewest byte values (0-255) in a list that counts as key material. */
export const BYTE_LIST_MIN_ITEMS = 16;

const REDACTION_MARKER = /^<redacted[\w-]*>$/;

const SECRET_NAME_PARTS = [
  'password',
  'passwd',
  'passphrase',
  'secret',
  'token',
  'mnemonic',
  'seed',
  'phrase',
  'privatekey',
  'apikey',
  'credential',
  'authorization',
  'cookie',
  'session',
  'ciphertext',
];
const SECRET_NAME_ENDINGS = [
  'key',
  'keys',
  'keypair',
  'pin',
  'pincode',
  'pwd',
  'auth',
  'jwt',
  'words',
];

/**
 * True when a field name says its value is a secret: it contains password,
 * passphrase, secret, token, mnemonic, seed, ... or ends in key, keys, pin,
 * auth, ... Case, `_`, `-` and `.` are ignored, so `privateKey`,
 * `PRIVATE_KEY` and `x-api-key` all match.
 */
export const isSecretFieldName = (name: string): boolean => {
  const normalized = String(name)
    .toLowerCase()
    .replace(/[^a-z0-9]/g, '');
  if (!normalized) return false;
  return (
    SECRET_NAME_PARTS.some((part) => normalized.includes(part)) ||
    SECRET_NAME_ENDINGS.some((ending) => normalized.endsWith(ending))
  );
};

type Range = [number, number];

const replaceRanges = (
  text: string,
  ranges: Range[],
  replacement: string | ((range: Range) => string)
): string => {
  if (ranges.length === 0) return text;
  let out = '';
  let cursor = 0;
  ranges.forEach((range) => {
    out += text.slice(cursor, range[0]);
    out += typeof replacement === 'string' ? replacement : replacement(range);
    [, cursor] = range;
  });
  return out + text.slice(cursor);
};

// ---------------------------------------------------------------------------
// Home and user-profile folders
// ---------------------------------------------------------------------------

const PATH_SEPARATORS = '[\\\\/]+';

const escapeForRegExp = (text: string) =>
  text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/** The user's own home folder, with `\`, `/` or JSON-escaped `\\`. */
const homeFolderPattern = (homeDir: string): RegExp | null => {
  const segments = homeDir.split(/[\\/]+/).filter(Boolean);
  if (segments.length === 0) return null;
  if (segments.length === 1 && /^[A-Za-z]:$/.test(segments[0])) return null;
  const leading = /^[\\/]/.test(homeDir) ? PATH_SEPARATORS : '';
  const body = segments.map(escapeForRegExp).join(PATH_SEPARATORS);
  return new RegExp(`${leading}${body}(?![\\w.-])`, 'gi');
};

// C:\Users\<name>, C:/Users/<name>, C:\\Users\\<name> and the old
// "Documents and Settings". Windows profile names may contain spaces.
const WINDOWS_PROFILE_FOLDER =
  /\b[A-Za-z]:[\\/]+(?:Users|Documents and Settings)[\\/]+[^\\/\r\n"'`<>|:*?[\]=,;+]+/gi;
// /c/Users/<name> (Git Bash, MSYS) and /mnt/c/Users/<name> (WSL).
const POSIX_STYLE_WINDOWS_PROFILE_FOLDER =
  /(?<![\w.~-])(?:\/mnt)?\/[A-Za-z]\/Users\/[^\\/\r\n"'`<>|:*?[\]=,;+]+/gi;
// /home/<name> (Linux) and /Users/<name> (macOS), but not inside a URL path.
const POSIX_HOME_FOLDER =
  /(?<=^|[\s"'`(=:;,[{<>]|file:\/\/)\/(?:home|Users)\/[^/\s"'`<>|:;,)\]}]+/gm;

// ---------------------------------------------------------------------------
// Tokens and credentials
// ---------------------------------------------------------------------------

const JWT = /\beyJ[\w-]{4,}\.eyJ[\w-]{4,}\.[\w-]*/g;
const AUTH_CREDENTIAL = /\b(Bearer|Basic)(\s+)([\w.~+/=-]{8,})/gi;

const looksLikeCredential = (value: string) =>
  /\d/.test(value) ||
  /[=+/_.~-]/.test(value) ||
  (/[a-z]/.test(value) && /[A-Z]/.test(value));

// ---------------------------------------------------------------------------
// Recovery phrases
// ---------------------------------------------------------------------------

type WordTest = (word: string) => boolean;

const wordlistTest = (wordlist: readonly string[]): WordTest => {
  const words = new Set(wordlist.map((word) => word.toLowerCase()));
  return (word) => words.has(word.toLowerCase());
};

const lowercaseWordTest: WordTest = (word) => /^[a-z]+$/.test(word);

/**
 * Replaces each run of RECOVERY_PHRASE_MIN_WORDS or more consecutive phrase
 * words with one marker. Anything that is not a letter separates words, so
 * spaces, commas, quotes, brackets, list numbers and line breaks all work.
 */
const redactRecoveryPhrases = (text: string, isPhraseWord: WordTest) => {
  const ranges: Range[] = [];
  let runStart = 0;
  let runEnd = 0;
  let runLength = 0;
  const endRun = () => {
    if (runLength >= RECOVERY_PHRASE_MIN_WORDS) ranges.push([runStart, runEnd]);
    runLength = 0;
  };

  for (const match of text.matchAll(/[A-Za-z]+/g)) {
    let word = match[0];
    let start = match.index ?? 0;
    // In JSON and util.inspect output a line break or tab is `\n`, `\r` or
    // `\t`, which glues its letter to the next word.
    if (start > 0 && text[start - 1] === '\\' && /^[nrt]./.test(word)) {
      word = word.slice(1);
      start += 1;
    }
    if (isPhraseWord(word)) {
      if (runLength === 0) runStart = start;
      runEnd = start + word.length;
      runLength += 1;
    } else {
      endRun();
    }
  }
  endRun();

  return replaceRanges(text, ranges, REDACTED_RECOVERY_PHRASE);
};

// ---------------------------------------------------------------------------
// Key material
// ---------------------------------------------------------------------------

// util.inspect of a Buffer: <Buffer 0c 22 ... 14 more bytes>
const BUFFER_DUMP =
  /<Buffer((?: [0-9a-f]{2})*)(?: \.\.\. (\d+) more bytes?)?>/g;
// [12, 34, ...] in JSON, `Uint8Array(64) [ 12, 34, ... ]` in util.inspect.
const BYTE_LIST = new RegExp(
  `\\b\\d{1,3}(?:\\s*,\\s*\\d{1,3}){${BYTE_LIST_MIN_ITEMS - 1},}\\b`,
  'g'
);
const HEX_RUN = new RegExp(
  `\\b(?:0x)?([0-9a-fA-F]{${KEY_MATERIAL_MIN_LENGTH},})\\b`,
  'g'
);
// base64, base64url and base58 share this alphabet.
const BASE64_RUN = new RegExp(
  `[A-Za-z0-9+/_-]{${KEY_MATERIAL_MIN_LENGTH},}={0,2}`,
  'g'
);

const redactKeyMaterial = (text: string) =>
  text
    .replace(BUFFER_DUMP, (_dump, bytes: string, more?: string) =>
      redactedBytes(bytes.length / 3 + (more ? Number(more) : 0))
    )
    .replace(BYTE_LIST, (list) => redactedBytes(list.split(',').length))
    .replace(HEX_RUN, (_run, hex: string) => redactedHex(hex.length))
    .replace(BASE64_RUN, (run) =>
      // Long words, paths and separator lines have no digits; random key
      // material of this length always has both digits and letters.
      /\d/.test(run) && /[A-Za-z]/.test(run) ? redactedBase64(run.length) : run
    );

// ---------------------------------------------------------------------------
// Secret fields in text: JSON, util.inspect output, key=value
// ---------------------------------------------------------------------------

// A field name, optionally quoted (also `\"name\"` inside a JSON string),
// followed by `:` or `=`.
const FIELD_NAME = /(\\?["'`]|)([A-Za-z_$][\w$.-]{0,63})\1[ \t]*[:=][ \t]*/g;
const VALUE_STOP = /[\s,;&"'`()[\]{}<>]/;
const QUOTES = '"\'`';
const OPENERS: Record<string, string> = { '[': ']', '{': '}', '(': ')' };
const CLOSERS = ')]}';
const KEPT_VALUES = new Set([
  '',
  'true',
  'false',
  'null',
  'undefined',
  '[]',
  '{}',
]);

/** Where the line containing `from` ends, before any `\r\n` or `\n`. */
const lineEnd = (text: string, from: number) => {
  const newline = text.indexOf('\n', from);
  if (newline === -1) return text.length;
  return newline > from && text[newline - 1] === '\r' ? newline - 1 : newline;
};

/**
 * Index just past the quoted string that starts at `start`, and whether the
 * string closed on its own line.
 */
const scanQuoted = (text: string, start: number): [number, boolean] => {
  const quote = text[start];
  for (let i = start + 1; i < text.length; i += 1) {
    const char = text[i];
    if (char === '\\') {
      i += 1;
    } else if (char === quote) {
      return [i + 1, true];
    } else if (char === '\n' || char === '\r') {
      return [i, false];
    }
  }
  return [text.length, false];
};

/**
 * Index just past the bracketed value that starts at `start`. The value may
 * run over several lines (util.inspect, pretty JSON) while each new line is
 * indented or closes a bracket. If it is broken (an unclosed string, or a
 * line that is neither, such as the next log entry), the value ends with the
 * line it started on, so one bad value never swallows the rest of a log.
 */
const bracketEnd = (text: string, start: number) => {
  const expected: string[] = [];
  let i = start;
  while (i < text.length) {
    const char = text[i];
    if (QUOTES.includes(char)) {
      const [end, closed] = scanQuoted(text, i);
      if (!closed) break;
      i = end;
    } else if (char === '\n' && !/^[\s)\]}]/.test(text.slice(i + 1, i + 2))) {
      break;
    } else {
      if (OPENERS[char]) {
        expected.push(OPENERS[char]);
      } else if (CLOSERS.includes(char)) {
        if (expected[expected.length - 1] !== char) return i + 1;
        expected.pop();
        if (expected.length === 0) return i + 1;
      }
      i += 1;
    }
  }
  return lineEnd(text, start);
};

/** Index just past the field value that starts at `start`. */
const valueEnd = (text: string, start: number): number => {
  const first = text[start];
  if (first === undefined) return start;
  if (QUOTES.includes(first)) return scanQuoted(text, start)[0];
  if (first === '\\' && QUOTES.includes(text[start + 1] ?? '')) {
    // A string inside a JSON string: \"...\" never spans lines.
    const stop = lineEnd(text, start);
    const close = text.slice(start + 2, stop).indexOf(`\\${text[start + 1]}`);
    return close === -1 ? stop : start + 2 + close + 2;
  }
  if (OPENERS[first]) return bracketEnd(text, start);
  if (first === '<') {
    const stop = lineEnd(text, start);
    const close = text.slice(start, stop).indexOf('>');
    return close === -1 ? stop : start + close + 1;
  }

  let end = start;
  while (end < text.length && !VALUE_STOP.test(text[end])) end += 1;

  // A value printed by util.inspect with its constructor name, such as
  // `Uint8Array(64) [ 1, 2, ... ]` or `Keypair { ... }`.
  if (/^[A-Za-z_$][\w$]*$/.test(text.slice(start, end))) {
    let next = end;
    const size = /^\(\d*\)/.exec(text.slice(next, next + 24));
    if (size) next += size[0].length;
    while (text[next] === ' ') next += 1;
    if (text[next] === '[' || text[next] === '{') return bracketEnd(text, next);
  }
  return end;
};

const unquote = (value: string) => {
  if (value.length >= 4 && value[0] === '\\' && QUOTES.includes(value[1])) {
    return value.slice(2, -2);
  }
  if (value.length >= 2 && QUOTES.includes(value[0])) {
    return value.slice(1, -1);
  }
  return value;
};

const isKeptValue = (value: string) => {
  const inner = unquote(value.trim()).trim();
  return KEPT_VALUES.has(inner) || REDACTION_MARKER.test(inner);
};

const redactedLike = (value: string) => {
  if (value.length >= 4 && value[0] === '\\' && QUOTES.includes(value[1])) {
    return `${value.slice(0, 2)}${REDACTED}${value.slice(0, 2)}`;
  }
  if (QUOTES.includes(value[0])) return `${value[0]}${REDACTED}${value[0]}`;
  return REDACTED;
};

const redactSecretFields = (text: string) => {
  const ranges: Range[] = [];
  const pattern = new RegExp(FIELD_NAME.source, 'g');
  let match = pattern.exec(text);
  while (match !== null) {
    if (isSecretFieldName(match[2])) {
      const start = match.index + match[0].length;
      const end = valueEnd(text, start);
      if (end > start) {
        if (!isKeptValue(text.slice(start, end))) ranges.push([start, end]);
        pattern.lastIndex = end;
      }
    }
    match = pattern.exec(text);
  }
  return replaceRanges(text, ranges, ([start, end]) =>
    redactedLike(text.slice(start, end))
  );
};

// ---------------------------------------------------------------------------
// Email addresses
// ---------------------------------------------------------------------------

const EMAIL_LOCAL_CHAR = /[A-Za-z0-9._%+-]/;
const EMAIL_DOMAIN_SOURCE = '[A-Za-z0-9-]+(?:\\.[A-Za-z0-9-]+)*\\.[A-Za-z]{2,}';

/** Linear scan from each `@`; a plain regex would backtrack on long runs. */
const redactEmails = (text: string) => {
  const ranges: Range[] = [];
  const domain = new RegExp(EMAIL_DOMAIN_SOURCE, 'y');
  let floor = 0;
  let at = text.indexOf('@');
  while (at !== -1) {
    let start = at;
    while (start > floor && EMAIL_LOCAL_CHAR.test(text[start - 1])) start -= 1;
    domain.lastIndex = at + 1;
    const found = start < at ? domain.exec(text) : null;
    if (found) {
      const end = at + 1 + found[0].length;
      ranges.push([start, end]);
      floor = end;
      at = text.indexOf('@', end);
    } else {
      at = text.indexOf('@', at + 1);
    }
  }
  return replaceRanges(text, ranges, REDACTED_EMAIL);
};

// ---------------------------------------------------------------------------
// Values (objects, arrays, errors)
// ---------------------------------------------------------------------------

const readProperty = (source: object, key: string): unknown => {
  try {
    return (source as Record<string, unknown>)[key];
  } catch {
    return '<unreadable>';
  }
};

const hasOwn = (object: object, key: string) =>
  Object.prototype.hasOwnProperty.call(object, key);

/** Also true for errors from another realm (vm contexts), unlike instanceof. */
export const isErrorLike = (value: unknown): value is Error =>
  value instanceof Error ||
  Object.prototype.toString.call(value) === '[object Error]';

const isBinary = (value: object): value is ArrayBufferView | ArrayBuffer =>
  ArrayBuffer.isView(value) ||
  value instanceof ArrayBuffer ||
  (typeof SharedArrayBuffer !== 'undefined' &&
    value instanceof SharedArrayBuffer);

const isByteArray = (values: unknown[]) =>
  values.length >= BYTE_LIST_MIN_ITEMS &&
  values.every(
    (value) =>
      typeof value === 'number' &&
      Number.isInteger(value) &&
      value >= 0 &&
      value <= 255
  );

/** A secret field keeps only values that cannot carry a secret. */
const secretFieldValue = (value: unknown): unknown =>
  value === null || value === undefined || typeof value === 'boolean'
    ? value
    : REDACTED;

/** The parts of an error worth keeping, as a plain object. */
const errorFields = (error: Error): Record<string, unknown> => {
  const fields: Record<string, unknown> = {
    name: readProperty(error, 'name'),
    message: readProperty(error, 'message'),
    stack: readProperty(error, 'stack'),
  };
  // Prefer the error's own serialization (AxiosError has one and leaves out
  // its sockets); otherwise its own enumerable properties (code, errno, ...).
  let source: object = error;
  const toJSON = readProperty(error, 'toJSON');
  if (typeof toJSON === 'function') {
    try {
      const json = toJSON.call(error);
      if (json && typeof json === 'object' && json !== error) source = json;
    } catch {
      // Use the own properties instead.
    }
  }
  Object.keys(source).forEach((key) => {
    if (!hasOwn(fields, key)) fields[key] = readProperty(source, key);
  });
  const cause = readProperty(error, 'cause');
  if (cause !== undefined && !hasOwn(fields, 'cause')) fields.cause = cause;
  const errors = readProperty(error, 'errors');
  if (Array.isArray(errors) && !hasOwn(fields, 'errors'))
    fields.errors = errors;
  return fields;
};

const defineField = (target: object, key: string, value: unknown) =>
  Object.defineProperty(target, key, {
    value,
    enumerable: true,
    writable: true,
    configurable: true,
  });

const valueScrubber = (scrubText: (text: string) => string) => {
  const visit = (
    value: unknown,
    path: Set<object>,
    seen: WeakSet<object>
  ): unknown => {
    switch (typeof value) {
      case 'string':
        return scrubText(value);
      case 'number':
        return Number.isFinite(value) ? value : String(value);
      case 'boolean':
      case 'undefined':
        return value;
      case 'bigint':
      case 'symbol':
        return scrubText(value.toString());
      case 'function':
        return `[function ${scrubText(value.name || 'anonymous')}]`;
      default:
        break;
    }
    if (value === null) return null;

    const object = value as object;
    if (path.has(object)) return '<circular>';
    if (seen.has(object)) return '<repeated>';
    if (isBinary(object)) return redactedBytes(object.byteLength);
    if (object instanceof Date) {
      return Number.isNaN(object.getTime())
        ? 'Invalid Date'
        : object.toISOString();
    }
    if (object instanceof RegExp) return scrubText(String(object));

    seen.add(object);
    path.add(object);
    try {
      const visitFields = (source: object) => {
        const out = {};
        Object.keys(source).forEach((key) => {
          const field = readProperty(source, key);
          defineField(
            out,
            scrubText(key),
            isSecretFieldName(key)
              ? secretFieldValue(field)
              : visit(field, path, seen)
          );
        });
        return out;
      };

      if (isErrorLike(object)) return visitFields(errorFields(object));
      if (Array.isArray(object)) {
        return isByteArray(object)
          ? redactedBytes(object.length)
          : object.map((item) => visit(item, path, seen));
      }
      if (object instanceof Map) {
        const entries = {};
        object.forEach((item, key) => defineField(entries, String(key), item));
        return visitFields(entries);
      }
      if (object instanceof Set) {
        return Array.from(object, (item) => visit(item, path, seen));
      }
      const toJSON = readProperty(object, 'toJSON');
      if (typeof toJSON === 'function') {
        try {
          const json = toJSON.call(object);
          if (json !== object) return visit(json, path, seen);
        } catch {
          // Fall back to the own properties.
        }
      }
      return visitFields(object);
    } finally {
      path.delete(object);
    }
  };

  return (value: unknown): unknown => {
    try {
      return visit(value, new Set(), new WeakSet());
    } catch {
      return '<unreadable>';
    }
  };
};

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

export interface ScrubberOptions {
  /** The user's home folder, shown as `~`. Defaults to os.homedir(). */
  homeDir?: string | null;
  /**
   * Recovery-phrase wordlist. Defaults to the BIP39 English list Hive already
   * ships. With `null`, any 12 or more consecutive lowercase words count.
   */
  wordlist?: readonly string[] | null;
}

export interface Scrubber {
  scrubText(text: string): string;
  /** A JSON-safe copy of `value` with every string scrubbed. */
  scrubValue(value: unknown): unknown;
}

const defaultHomeDir = () => {
  try {
    return os.homedir();
  } catch {
    return null;
  }
};

const defaultWordlist = (): readonly string[] | null => {
  const english = wordlists?.english;
  return Array.isArray(english) && english.length > 0 ? english : null;
};

export const createScrubber = (options: ScrubberOptions = {}): Scrubber => {
  const homeDir =
    options.homeDir === undefined ? defaultHomeDir() : options.homeDir;
  const homeFolder = homeDir ? homeFolderPattern(homeDir) : null;
  const wordlist =
    options.wordlist === undefined ? defaultWordlist() : options.wordlist;
  const isPhraseWord = wordlist ? wordlistTest(wordlist) : lowercaseWordTest;

  const scrubText = (input: string): string => {
    let text = typeof input === 'string' ? input : String(input ?? '');
    text = text
      .replace(WINDOWS_PROFILE_FOLDER, '~')
      .replace(POSIX_STYLE_WINDOWS_PROFILE_FOLDER, '~');
    if (homeFolder) text = text.replace(homeFolder, '~');
    text = text
      .replace(POSIX_HOME_FOLDER, '~')
      .replace(JWT, REDACTED_JWT)
      .replace(
        AUTH_CREDENTIAL,
        (whole, scheme: string, space: string, credential: string) =>
          looksLikeCredential(credential)
            ? `${scheme}${space}${REDACTED}`
            : whole
      );
    text = redactRecoveryPhrases(text, isPhraseWord);
    text = redactKeyMaterial(text);
    text = redactSecretFields(text);
    return redactEmails(text);
  };

  return { scrubText, scrubValue: valueScrubber(scrubText) };
};

let defaultScrubber: Scrubber | null = null;
const getDefaultScrubber = () => {
  if (!defaultScrubber) defaultScrubber = createScrubber();
  return defaultScrubber;
};

/** Scrubs text with the defaults: this user's home folder, BIP39 English. */
export const scrubText = (text: string): string =>
  getDefaultScrubber().scrubText(text);

/** Scrubs a value with the defaults; the result is safe for JSON.stringify. */
export const scrubValue = (value: unknown): unknown =>
  getDefaultScrubber().scrubValue(value);
