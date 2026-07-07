import '@testing-library/jest-dom';
import 'jest-canvas-mock';
import { TextDecoder, TextEncoder } from 'util';

(global as any).TextDecoder = TextDecoder;
(global as any).TextEncoder = TextEncoder;

const MAX_TIMEOUT_FOR_TESTS = 10 * 1000;

jest.setTimeout(MAX_TIMEOUT_FOR_TESTS);
