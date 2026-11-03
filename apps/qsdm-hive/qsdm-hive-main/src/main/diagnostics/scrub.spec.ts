/**
 * @jest-environment node
 */
/* eslint-disable @cspell/spellchecker */
import crypto from 'crypto';
import util from 'util';
import vm from 'vm';

import { entropyToMnemonic } from 'bip39';
import bs58 from 'bs58';

import {
  createScrubber,
  isSecretFieldName,
  REDACTED,
  REDACTED_EMAIL,
  REDACTED_JWT,
  REDACTED_RECOVERY_PHRASE,
} from './scrub';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Json = Record<string, any>;

// Deterministic, real-shaped secrets, so a failure always reproduces.
const sha = (algorithm: string, label: string) =>
  crypto.createHash(algorithm).update(label).digest();

const WINDOWS_HOME = 'C:\\Users\\Alice Example';
const POSIX_HOME = '/home/alice';

// BIP39 English test vectors (trezor/python-mnemonic).
const PHRASE_12 =
  'legal winner thank year wave sausage worth useful legal winner thank yellow';
const PHRASE_24 =
  'void come effort suffer camp survey warrior heavy shoot primary clutch crush open amazing screen patrol group space point ten exist slush involve unfold';
const PHRASE_GENERATED = entropyToMnemonic(
  sha('sha256', 'hive diagnostics phrase').toString('hex')
);
const DISTINCTIVE_PHRASE_WORDS = [
  'sausage',
  'yellow',
  'winner',
  'useful',
  'slush',
  'unfold',
  'warrior',
  'patrol',
  'clutch',
];

const PRIVATE_KEY_HEX = sha('sha256', 'hive private key').toString('hex');
const ML_DSA_KEY_HEX = Buffer.concat(
  Array.from({ length: 77 }, (_, i) => sha('sha512', `ml-dsa ${i}`))
).toString('hex');
const SECRET_KEY_BYTES = sha('sha512', 'hive ed25519 secret key');
const SECRET_KEY_BASE58 = bs58.encode(SECRET_KEY_BYTES);
const CIPHERTEXT_BASE64 = Buffer.concat([
  sha('sha512', 'keystore ciphertext a'),
  sha('sha256', 'keystore ciphertext b'),
]).toString('base64');
const base64url = (value: string | Buffer) =>
  Buffer.from(value).toString('base64url');
const JWT_TOKEN = [
  base64url(JSON.stringify({ alg: 'HS256', typ: 'JWT' })),
  base64url(JSON.stringify({ sub: 'hive-account-7', iat: 1793500000 })),
  base64url(sha('sha256', 'jwt signature')),
].join('.');
const BEARER_TOKEN = base64url(sha('sha256', 'bearer token').subarray(0, 24));
const PASSWORD = 'hunter2-Correct-Horse';
const PASSPHRASE = 'Tr0ub4dor&3 battery staple';
const PIN = '482916';
const EMAIL = 'alice.example+hive@example.co.uk';

const SECRETS = [
  PHRASE_12,
  PHRASE_24,
  PHRASE_GENERATED,
  PRIVATE_KEY_HEX,
  ML_DSA_KEY_HEX,
  SECRET_KEY_BASE58,
  CIPHERTEXT_BASE64,
  JWT_TOKEN,
  BEARER_TOKEN,
  PASSWORD,
  PASSPHRASE,
  PIN,
  EMAIL,
  'Alice Example',
  'alice.example',
];

const wordWindows = (phrase: string, size: number) => {
  const words = phrase.split(' ');
  return words
    .slice(0, words.length - size + 1)
    .map((_, i) => words.slice(i, i + size).join(' '));
};

const expectNoSecrets = (output: string) => {
  SECRETS.forEach((secret) => expect(output).not.toContain(secret));
  DISTINCTIVE_PHRASE_WORDS.forEach((word) =>
    expect(output.toLowerCase()).not.toContain(word)
  );
  wordWindows(PHRASE_GENERATED, 3).forEach((window) =>
    expect(output).not.toContain(window)
  );
  const firstBytes = Array.from(SECRET_KEY_BYTES.subarray(0, 8));
  expect(output).not.toContain(firstBytes.join(','));
  expect(output).not.toContain(firstBytes.join(', '));
  expect(output).not.toContain(SECRET_KEY_BYTES.subarray(0, 8).toString('hex'));
  expect(output).not.toMatch(/(?:\b[0-9a-f]{2} ){15}/);
};

describe('scrubber', () => {
  const scrubber = createScrubber({ homeDir: WINDOWS_HOME });
  const posix = createScrubber({ homeDir: POSIX_HOME });
  const scrub = scrubber.scrubText;

  describe('recovery phrases', () => {
    it('replaces 12- and 24-word phrases in free text', () => {
      expect(scrub(`words: ${PHRASE_12}.`)).toBe(
        `words: ${REDACTED_RECOVERY_PHRASE}.`
      );
      expect(scrub(`Import failed (${PHRASE_24})`)).toBe(
        `Import failed (${REDACTED_RECOVERY_PHRASE})`
      );
      expect(scrub(PHRASE_GENERATED)).toBe(REDACTED_RECOVERY_PHRASE);
    });

    it('finds phrases whatever separates or cases the words', () => {
      const words = PHRASE_12.split(' ');
      expect(scrub(JSON.stringify(words))).toBe(
        `["${REDACTED_RECOVERY_PHRASE}"]`
      );
      expect(scrub(util.inspect(words))).toContain(REDACTED_RECOVERY_PHRASE);
      expect(scrub(words.join(', '))).toBe(REDACTED_RECOVERY_PHRASE);
      expect(scrub(words.map((w, i) => `${i + 1}. ${w}`).join('\n'))).toBe(
        `1. ${REDACTED_RECOVERY_PHRASE}`
      );
      expect(scrub(PHRASE_12.toUpperCase())).toBe(REDACTED_RECOVERY_PHRASE);
      // A phrase split by line breaks inside a JSON string: "legal\nwinner".
      expect(scrub(JSON.stringify(words.join('\n')))).toBe(
        `"${REDACTED_RECOVERY_PHRASE}"`
      );
    });

    it('keeps shorter runs and ordinary sentences', () => {
      const eleven = PHRASE_12.split(' ').slice(0, 11).join(' ');
      expect(scrub(eleven)).toBe(eleven);
      const sentence =
        'Task 7 failed: the current file was not found on this computer, so Hive will retry in 30 seconds.';
      expect(scrub(sentence)).toBe(sentence);
    });

    it('falls back to 12 lowercase words when no wordlist is available', () => {
      const fallback = createScrubber({ homeDir: null, wordlist: null });
      expect(
        fallback.scrubText(
          'one two three four five six seven eight nine ten eleven twelve'
        )
      ).toBe(REDACTED_RECOVERY_PHRASE);
      const eleven = 'one two three four five six seven eight nine ten eleven';
      expect(fallback.scrubText(eleven)).toBe(eleven);
    });
  });

  describe('key material', () => {
    it('replaces long hex runs, with or without 0x', () => {
      expect(scrub(`signing with ${PRIVATE_KEY_HEX} now`)).toBe(
        'signing with <redacted-hex-64> now'
      );
      expect(scrub(`0x${PRIVATE_KEY_HEX}`)).toBe('<redacted-hex-64>');
      expect(scrub(ML_DSA_KEY_HEX)).toBe(
        `<redacted-hex-${ML_DSA_KEY_HEX.length}>`
      );
    });

    it('replaces long base58 and base64 runs', () => {
      expect(scrub(`secret ${SECRET_KEY_BASE58} end`)).toBe(
        `secret <redacted-base64-${SECRET_KEY_BASE58.length}> end`
      );
      expect(scrub(`ciphertext ${CIPHERTEXT_BASE64}`)).toBe(
        `ciphertext <redacted-base64-${CIPHERTEXT_BASE64.length}>`
      );
    });

    it('replaces byte lists, typed-array dumps and Buffer dumps', () => {
      const bytes = Array.from(SECRET_KEY_BYTES);
      expect(scrub(JSON.stringify(bytes))).toBe('[<redacted-bytes-64>]');
      const inspected = scrub(util.inspect(new Uint8Array(bytes)));
      expect(inspected).toContain('<redacted-bytes-64>');
      expect(inspected).not.toMatch(/\d{2,3},/);
      expect(scrub(util.inspect(Buffer.from(SECRET_KEY_BYTES)))).toBe(
        '<redacted-bytes-64>'
      );
    });

    it('keeps short ids, nonces, versions and number lists', () => {
      const nonce = sha('sha256', 'nonce').toString('hex').slice(0, 32);
      const almostKey = PRIVATE_KEY_HEX.slice(0, 63);
      const taskId = bs58.encode(sha('sha256', 'task id'));
      const text = `nonce ${nonce} task ${taskId} hive 1.4.22 near-key ${almostKey} ports 80, 443, 8080`;
      expect(scrub(text)).toBe(text);
      const separator = '-'.repeat(80);
      expect(scrub(separator)).toBe(separator);
    });
  });

  describe('tokens and credentials', () => {
    it('replaces JWTs and Bearer or Basic credentials', () => {
      expect(scrub(`session ${JWT_TOKEN} expired`)).toBe(
        `session ${REDACTED_JWT} expired`
      );
      const header = scrub(`Authorization: Bearer ${BEARER_TOKEN}`);
      expect(header).not.toContain(BEARER_TOKEN);
      expect(header).toContain(REDACTED);
      const basic = scrub(
        'curl -H "Authorization: Basic dXNlcjpwYXNzd29yZA=="'
      );
      expect(basic).not.toContain('dXNlcjpwYXNzd29yZA');
    });

    it('keeps prose that only mentions the schemes', () => {
      const text = 'Basic information about the bearer of this wallet.';
      expect(scrub(text)).toBe(text);
    });
  });

  describe('secret fields', () => {
    it('redacts secret values in JSON and keeps the rest', () => {
      const json = JSON.stringify({
        password: PASSWORD,
        username: 'bob',
        passphrase: PASSPHRASE,
        mnemonic: 'x y',
        hasPin: true,
        pin: Number(PIN),
        keyId: 'k-1',
        publicKey: 'pk',
        apiKey: 'abc123',
        seedHex: '00ff',
        token: null,
      });
      expect(scrub(json)).toBe(
        '{"password":"<redacted>","username":"bob","passphrase":"<redacted>",' +
          '"mnemonic":"<redacted>","hasPin":true,"pin":<redacted>,"keyId":"k-1",' +
          '"publicKey":"<redacted>","apiKey":"<redacted>","seedHex":"<redacted>",' +
          '"token":null}'
      );
    });

    it('redacts secret values in util.inspect output', () => {
      const out = scrub(
        util.inspect({
          password: PASSWORD,
          wallet: { secretKey: new Uint8Array([1, 2, 3]), label: 'main' },
        })
      );
      expect(out).toContain("password: '<redacted>'");
      expect(out).toContain('secretKey: <redacted>');
      expect(out).toContain("label: 'main'");
      expect(out).not.toContain(PASSWORD);
    });

    it('redacts key=value pairs, query strings and command-line flags', () => {
      expect(scrub(`QSDM_HIVE_TOKEN=abc123 PASSWORD=${PIN} user=bob`)).toBe(
        'QSDM_HIVE_TOKEN=<redacted> PASSWORD=<redacted> user=bob'
      );
      expect(
        scrub('GET https://api.example.com/v1/tasks?token=abc123&page=2')
      ).toBe('GET https://api.example.com/v1/tasks?token=<redacted>&page=2');
      expect(
        scrub(
          `--operator-passphrase-file=${WINDOWS_HOME}\\pp.txt --keystore=wallet.json`
        )
      ).toBe('--operator-passphrase-file=<redacted> --keystore=wallet.json');
      expect(scrub(`passphrase="${PASSPHRASE}"`)).toBe(
        'passphrase="<redacted>"'
      );
    });

    it('redacts fields inside a JSON string inside JSON', () => {
      const doubled = JSON.stringify({
        body: JSON.stringify({ password: PASSWORD, ok: 1 }),
      });
      expect(scrub(doubled)).toBe(
        '{"body":"{\\"password\\":\\"<redacted>\\",\\"ok\\":1}"}'
      );
    });

    it('follows a value over indented lines and stops at the next entry', () => {
      const dump = [
        '[2026-11-03 05:15:30.123] [info]  wallet { secretKey: Uint8Array(4) [',
        '    11, 22,',
        '    33, 44',
        "  ], label: 'main' }",
        '[2026-11-03 05:15:31.000] [info]  next entry',
      ].join('\n');
      expect(scrub(dump)).toBe(
        [
          "[2026-11-03 05:15:30.123] [info]  wallet { secretKey: <redacted>, label: 'main' }",
          '[2026-11-03 05:15:31.000] [info]  next entry',
        ].join('\n')
      );
    });

    it('never lets a broken value swallow the following lines', () => {
      expect(scrub('token: [unclosed\n[info] later line')).toBe(
        'token: <redacted>\n[info] later line'
      );
      expect(scrub("password: (user's choice) and more\nnext line")).toBe(
        'password: <redacted>\nnext line'
      );
      expect(scrub('secret: (unclosed\r\nnext line')).toBe(
        'secret: <redacted>\r\nnext line'
      );
    });

    it('recognises secret field names', () => {
      [
        'password',
        'newPassword',
        'PASSPHRASE',
        'clientSecret',
        'access_token',
        'mnemonic',
        'seedPhrase',
        'privateKey',
        'secretKey',
        'x-api-key',
        'pin',
        'authorization',
        'Cookie',
        'sessionId',
        'recoveryWords',
      ].forEach((name) => expect(isSecretFieldName(name)).toBe(true));
      [
        'username',
        'keyId',
        'keystorePath',
        'message',
        'stack',
        'reason',
        'exitCode',
        'mapping',
      ].forEach((name) => expect(isSecretFieldName(name)).toBe(false));
    });
  });

  describe('email addresses', () => {
    it('replaces addresses and keeps package names and versions', () => {
      expect(scrub(`Contact ${EMAIL} or bob@example.com.`)).toBe(
        `Contact ${REDACTED_EMAIL} or ${REDACTED_EMAIL}.`
      );
      const packages =
        'node_modules/@aws-sdk/client-s3 electron@43.1.1 @cloudbet/market-helper';
      expect(scrub(packages)).toBe(packages);
    });
  });

  describe('home folders', () => {
    it('replaces Windows profile folders in every spelling', () => {
      expect(
        scrub(`${WINDOWS_HOME}\\AppData\\Roaming\\QSDM-Hive\\logs\\main.log`)
      ).toBe('~\\AppData\\Roaming\\QSDM-Hive\\logs\\main.log');
      expect(scrub('C:/Users/Alice Example/AppData')).toBe('~/AppData');
      expect(scrub(JSON.stringify(`${WINDOWS_HOME}\\AppData`))).toBe(
        '"~\\\\AppData"'
      );
      expect(scrub('file:///C:/Users/Alice%20Example/app/index.html')).toBe(
        'file:///~/app/index.html'
      );
      expect(scrub('C:\\Users\\ALICEE~1\\AppData\\Local\\Temp')).toBe(
        '~\\AppData\\Local\\Temp'
      );
      expect(scrub('D:\\Users\\bob\\x.txt')).toBe('~\\x.txt');
      expect(scrub('/c/Users/Alice Example/app')).toBe('~/app');
      expect(scrub('/mnt/c/Users/alice/app')).toBe('~/app');
    });

    it('replaces Linux and macOS home folders', () => {
      expect(posix.scrubText('/home/alice/.config/QSDM-Hive/main.log')).toBe(
        '~/.config/QSDM-Hive/main.log'
      );
      expect(
        posix.scrubText(
          '    at Object.<anonymous> (/home/alice/app/main.js:10:5)'
        )
      ).toBe('    at Object.<anonymous> (~/app/main.js:10:5)');
      expect(posix.scrubText('cwd=/Users/alice/Library/Logs')).toBe(
        'cwd=~/Library/Logs'
      );
      expect(posix.scrubText('file:///home/bob/x')).toBe('file://~/x');
      const custom = createScrubber({ homeDir: '/var/lib/hive-user' });
      expect(custom.scrubText('/var/lib/hive-user/data')).toBe('~/data');
      expect(custom.scrubText('/var/lib/hive-users/data')).toBe(
        '/var/lib/hive-users/data'
      );
    });

    it('keeps URL paths that merely contain /home/ or /Users/', () => {
      const url =
        'https://example.com/home/bob/page and https://example.com/Users/x';
      expect(posix.scrubText(url)).toBe(url);
    });

    it("hides the user's own home folder even inside a URL", () => {
      expect(posix.scrubText('https://example.com/home/alice/page')).toBe(
        'https://example.com~/page'
      );
    });
  });

  describe('values', () => {
    it('scrubs nested objects, arrays, errors and binary data', () => {
      const error = Object.assign(
        new Error(
          `Import failed for ${PHRASE_12} at ${WINDOWS_HOME}\\wallet.json`
        ),
        { code: 'E_IMPORT', password: PASSWORD }
      );
      (error as Error & { cause?: unknown }).cause = new Error(
        `key ${PRIVATE_KEY_HEX} mailed to ${EMAIL}`
      );

      const scrubbed = scrubber.scrubValue({
        wallet: {
          secretKey: Array.from(SECRET_KEY_BYTES),
          mnemonic: PHRASE_24,
          label: 'main wallet',
        },
        bytes: Buffer.from(SECRET_KEY_BYTES),
        typed: new Uint8Array(SECRET_KEY_BYTES),
        list: [JWT_TOKEN, { password: PASSWORD }, SECRET_KEY_BASE58],
        error,
      }) as Json;

      expect(scrubbed.wallet).toEqual({
        secretKey: REDACTED,
        mnemonic: REDACTED,
        label: 'main wallet',
      });
      expect(scrubbed.bytes).toBe('<redacted-bytes-64>');
      expect(scrubbed.typed).toBe('<redacted-bytes-64>');
      expect(scrubbed.list[0]).toBe(REDACTED_JWT);
      expect(scrubbed.list[1]).toEqual({ password: REDACTED });
      expect(scrubbed.error.name).toBe('Error');
      expect(scrubbed.error.code).toBe('E_IMPORT');
      expect(scrubbed.error.password).toBe(REDACTED);
      expect(scrubbed.error.message).toBe(
        `Import failed for ${REDACTED_RECOVERY_PHRASE} at ~\\wallet.json`
      );
      expect(scrubbed.error.stack).toContain(REDACTED_RECOVERY_PHRASE);
      expect(scrubbed.error.cause.message).toBe(
        `key <redacted-hex-64> mailed to ${REDACTED_EMAIL}`
      );
      expectNoSecrets(JSON.stringify(scrubbed));
    });

    it("uses an error's own toJSON and leaves out what it leaves out", () => {
      class FakeAxiosError extends Error {
        request: Record<string, unknown> = {};

        constructor() {
          super('Request failed with status code 401');
          this.name = 'AxiosError';
          this.request.socket = { request: this.request };
        }

        toJSON() {
          return {
            message: this.message,
            code: 'ERR_BAD_REQUEST',
            config: { headers: { Authorization: `Bearer ${BEARER_TOKEN}` } },
          };
        }
      }
      const scrubbed = scrubber.scrubValue(new FakeAxiosError()) as Json;
      expect(scrubbed.name).toBe('AxiosError');
      expect(scrubbed.code).toBe('ERR_BAD_REQUEST');
      expect(scrubbed.config.headers.Authorization).toBe(REDACTED);
      expect(scrubbed).not.toHaveProperty('request');
    });

    it('reads errors from another realm', () => {
      const foreign = vm.runInNewContext('new TypeError("bad key " + key)', {
        key: PRIVATE_KEY_HEX,
      });
      expect(foreign instanceof Error).toBe(false);
      const scrubbed = scrubber.scrubValue(foreign) as Json;
      expect(scrubbed.name).toBe('TypeError');
      expect(scrubbed.message).toBe('bad key <redacted-hex-64>');
      expect(scrubbed.stack).not.toContain(PRIVATE_KEY_HEX);
    });

    it('survives cycles, repeats, odd types and throwing getters', () => {
      const shared = { x: 1 };
      const loop: Record<string, unknown> = { name: 'loop' };
      loop.self = loop;
      const awkward = {
        get broken() {
          throw new Error('no');
        },
      };
      const scrubbed = scrubber.scrubValue({
        loop,
        a: shared,
        b: shared,
        awkward,
        when: new Date(0),
        big: BigInt(42),
        nan: Number.NaN,
        fn: function namedFunction() {
          return 1;
        },
        map: new Map([
          ['token', 'abc'],
          ['plain', 'ok'],
        ]),
        set: new Set([1, 2]),
        small: [1, 2, 3],
      });
      expect(scrubbed).toEqual({
        loop: { name: 'loop', self: '<circular>' },
        a: { x: 1 },
        b: '<repeated>',
        awkward: { broken: '<unreadable>' },
        when: '1970-01-01T00:00:00.000Z',
        big: '42',
        nan: 'NaN',
        fn: '[function namedFunction]',
        map: { token: REDACTED, plain: 'ok' },
        set: [1, 2],
        small: [1, 2, 3],
      });
      expect(() => JSON.stringify(scrubbed)).not.toThrow();
    });
  });

  it('leaves nothing seeded in messages, stacks and nested objects', () => {
    expect.hasAssertions();
    const error = new Error(
      [
        `seed: ${PHRASE_12}`,
        `mnemonic=${PHRASE_GENERATED}`,
        `privateKey=${PRIVATE_KEY_HEX}`,
        `pin=${PIN}`,
        `owner ${EMAIL}`,
        `profile ${WINDOWS_HOME}\\AppData`,
      ].join('; ')
    );
    const nested = {
      level1: {
        level2: {
          wallet: {
            secretKey: Array.from(SECRET_KEY_BYTES),
            note: PHRASE_24,
            backup: SECRET_KEY_BASE58,
          },
          headers: {
            Authorization: `Bearer ${BEARER_TOKEN}`,
            Cookie: `session=${JWT_TOKEN}`,
          },
          config: { passphrase: PASSPHRASE, password: PASSWORD },
          blobs: [
            ML_DSA_KEY_HEX,
            CIPHERTEXT_BASE64,
            Buffer.from(SECRET_KEY_BYTES),
          ],
        },
      },
      error,
    };

    const asValue = JSON.stringify(scrubber.scrubValue(nested));
    const asJsonText = scrub(JSON.stringify({ ...nested, stack: error.stack }));
    const asInspectText = scrub(
      util.inspect(nested, { depth: null, maxArrayLength: null })
    );
    const asLogLine = scrub(
      `[2026-11-03 05:15:30.123] [error] Unhandled Rejection -> ${error.stack}`
    );

    [asValue, asJsonText, asInspectText, asLogLine].forEach(expectNoSecrets);
  });

  it('gives the same result when run twice', () => {
    const text = util.inspect(
      {
        phrase: PHRASE_12,
        loose: `${PHRASE_24} and ${PRIVATE_KEY_HEX} for ${EMAIL}`,
        path: `${WINDOWS_HOME}\\AppData`,
        token: JWT_TOKEN,
        bytes: Buffer.from(SECRET_KEY_BYTES),
      },
      { depth: null }
    );
    const once = scrub(text);
    expect(scrub(once)).toBe(once);
  });

  it('scrubs a large log in linear time, including hostile input', () => {
    const line = `[2026-11-03 05:15:30.123] [info] task 7 ok path=${WINDOWS_HOME}\\AppData key=${PRIVATE_KEY_HEX} user ${EMAIL} ${PHRASE_12}\n`;
    const hostile = [
      'a'.repeat(200000),
      'a-'.repeat(100000),
      `${'1,'.repeat(10)}x`.repeat(20000),
      'Aa1'.repeat(70000),
      `x${'@'.repeat(50000)}`,
      `token: ${'['.repeat(20000)}`,
      'secret: [unclosed\n'.repeat(20000),
      `password: \\"${'x'.repeat(100)}\n`.repeat(20000),
      '"'.repeat(50000),
    ].join('\n');
    const log = line.repeat(Math.ceil(2_000_000 / line.length)) + hostile;

    const started = Date.now();
    const out = scrub(log);
    const elapsed = Date.now() - started;

    expectNoSecrets(out);
    expect(elapsed).toBeLessThan(30000);
  }, 60000);
});
