import axios from 'axios';

const DEFAULT_TIMEOUT_MS = 8_000;
const DEFAULT_CIRCUIT_OPEN_MS = 20_000;
const ENDPOINT_HEALTH_TTL_MS = 15_000;
const FAILURE_WINDOW_MS = 30_000;
const FAILURES_BEFORE_OPEN = 2;

type CircuitState = {
  openUntil: number;
  lastError: string;
  consecutiveFailures: number;
  lastFailureAt: number;
};

type QsdmGetOptions = {
  timeout?: number;
  bypassCircuit?: boolean;
};

const circuits = new Map<string, CircuitState>();
const inFlightRequests = new Map<string, Promise<unknown>>();
const endpointHealthyUntil = new Map<string, number>();
const endpointProbes = new Map<string, Promise<void>>();

const apiBaseKey = (rawUrl: string) => {
  try {
    const url = new URL(rawUrl);
    const apiMarker = '/api/v1';
    const markerIndex = url.pathname.indexOf(apiMarker);
    const apiPath =
      markerIndex >= 0
        ? url.pathname.slice(0, markerIndex + apiMarker.length)
        : url.pathname;
    return `${url.origin}${apiPath.replace(/\/+$/, '')}`;
  } catch {
    return rawUrl;
  }
};

export const getQsdmReadErrorMessage = (error: unknown) => {
  if (typeof axios.isAxiosError === 'function' && axios.isAxiosError(error)) {
    const status = error.response?.status;
    const data = error.response?.data as
      | { error?: string; message?: string }
      | string
      | undefined;
    const responseMessage =
      typeof data === 'string'
        ? data
        : data?.message || data?.error || error.message;
    return status
      ? `HTTP ${status}${responseMessage ? `: ${responseMessage}` : ''}`
      : error.message;
  }
  return error instanceof Error ? error.message : String(error);
};

const isTransientReadFailure = (error: unknown) => {
  const status = (error as { response?: { status?: number } })?.response
    ?.status;
  return (
    status === undefined || status === 408 || status === 429 || status >= 500
  );
};

const circuitOpenError = (baseKey: string, circuit: CircuitState) => {
  const remainingSeconds = Math.max(
    1,
    Math.ceil((circuit.openUntil - Date.now()) / 1000)
  );
  return Object.assign(
    new Error(
      `${baseKey} is temporarily unavailable; retrying in ${remainingSeconds}s (${circuit.lastError})`
    ),
    { code: 'QSDM_READ_CIRCUIT_OPEN' }
  );
};

export const qsdmGetJson = async <T>(
  url: string,
  { timeout = DEFAULT_TIMEOUT_MS, bypassCircuit = false }: QsdmGetOptions = {}
): Promise<T> => {
  const baseKey = apiBaseKey(url);
  const circuit = circuits.get(baseKey);
  if (!bypassCircuit && circuit && circuit.openUntil > Date.now()) {
    throw circuitOpenError(baseKey, circuit);
  }
  if (circuit?.openUntil && circuit.openUntil <= Date.now()) {
    circuits.delete(baseKey);
  }

  const requestKey = `${url}|${timeout}`;
  const existingRequest = inFlightRequests.get(requestKey) as
    | Promise<T>
    | undefined;
  if (existingRequest) {
    return existingRequest;
  }

  const endpointIsRecentlyHealthy =
    (endpointHealthyUntil.get(baseKey) || 0) > Date.now();
  if (!bypassCircuit && !endpointIsRecentlyHealthy) {
    const existingProbe = endpointProbes.get(baseKey);
    if (existingProbe) {
      await existingProbe;
      const postProbeCircuit = circuits.get(baseKey);
      if (postProbeCircuit && postProbeCircuit.openUntil > Date.now()) {
        throw circuitOpenError(baseKey, postProbeCircuit);
      }
    }
  }

  const request = axios
    .get<T>(url, { timeout })
    .then((response) => {
      circuits.delete(baseKey);
      endpointHealthyUntil.set(baseKey, Date.now() + ENDPOINT_HEALTH_TTL_MS);
      return response.data;
    })
    .catch((error) => {
      if (isTransientReadFailure(error)) {
        const now = Date.now();
        const previous = circuits.get(baseKey);
        const failuresAreConsecutive =
          previous && now - previous.lastFailureAt <= FAILURE_WINDOW_MS;
        const consecutiveFailures = failuresAreConsecutive
          ? previous.consecutiveFailures + 1
          : 1;
        const endpointWasRecentlyHealthy =
          (endpointHealthyUntil.get(baseKey) || 0) > now;
        circuits.set(baseKey, {
          openUntil:
            !endpointWasRecentlyHealthy &&
            consecutiveFailures >= FAILURES_BEFORE_OPEN
              ? now + DEFAULT_CIRCUIT_OPEN_MS
              : 0,
          lastError: getQsdmReadErrorMessage(error),
          consecutiveFailures,
          lastFailureAt: now,
        });
      }
      throw error;
    })
    .finally(() => {
      inFlightRequests.delete(requestKey);
    });

  inFlightRequests.set(requestKey, request);
  if (
    !bypassCircuit &&
    (endpointHealthyUntil.get(baseKey) || 0) <= Date.now() &&
    !endpointProbes.has(baseKey)
  ) {
    const probe = request
      .then(
        () => undefined,
        () => undefined
      )
      .finally(() => {
        endpointProbes.delete(baseKey);
      });
    endpointProbes.set(baseKey, probe);
  }
  return request;
};

export const qsdmGetFirstJson = async <T>(
  urls: string[],
  options: QsdmGetOptions = {}
): Promise<T> => {
  const errors: string[] = [];

  for (const url of Array.from(new Set(urls))) {
    try {
      return await qsdmGetJson<T>(url, options);
    } catch (error) {
      errors.push(`${url}: ${getQsdmReadErrorMessage(error)}`);
    }
  }

  throw new Error(errors.join('; '));
};

export const clearQsdmReadCircuitState = () => {
  circuits.clear();
  inFlightRequests.clear();
  endpointHealthyUntil.clear();
  endpointProbes.clear();
};
