import axios from 'axios';

import { buildQsdmCoreApiUrl } from 'config/qsdm';

// Proof-of-Entanglement parents for signed CELL transfers.
//
// A transfer's parent_cells are covered by its signature. Once the network's
// PoE activation height is reached, validators refuse a transfer unless it
// names at least two transactions already committed in the last 8640 blocks.
// The node's newest committed transactions satisfy that rule both before and
// after activation, so Hive always sends them.

export const QSDM_POE_MIN_PARENTS = 2;

const PARENT_ID_PATTERN = /^[0-9A-Za-z_-]{16,128}$/;

export const isQsdmParentId = (value: unknown): value is string =>
  typeof value === 'string' && PARENT_ID_PATTERN.test(value);

interface QsdmChainParentsResponse {
  parents?: unknown;
}

interface QsdmReceiptsListResponse {
  receipts?: Array<{ tx_id?: unknown; status?: unknown }>;
}

const pickParents = (ids: unknown[]): string[] => {
  const picked: string[] = [];
  ids.forEach((id) => {
    if (
      picked.length < QSDM_POE_MIN_PARENTS &&
      isQsdmParentId(id) &&
      !picked.includes(id)
    ) {
      picked.push(id);
    }
  });
  return picked.length === QSDM_POE_MIN_PARENTS ? picked : [];
};

const fromParentsEndpoint = async (): Promise<string[]> => {
  const response = await axios.get<QsdmChainParentsResponse>(
    buildQsdmCoreApiUrl('/chain/parents'),
    { timeout: 10000 }
  );
  const { parents } = response.data ?? {};
  return Array.isArray(parents) ? pickParents(parents) : [];
};

// Nodes released before GET /chain/parents still list receipts, newest
// first; a successful receipt (status 1) means the transaction is in a block.
const fromReceipts = async (): Promise<string[]> => {
  const url = new URL(buildQsdmCoreApiUrl('/receipts'));
  url.searchParams.set('limit', '32');
  const response = await axios.get<QsdmReceiptsListResponse>(url.toString(), {
    timeout: 10000,
  });
  const receipts = Array.isArray(response.data?.receipts)
    ? response.data.receipts
    : [];
  return pickParents(
    receipts
      .filter((receipt) => receipt?.status === 1)
      .map((receipt) => receipt.tx_id)
  );
};

const isMissingRoute = (error: unknown) =>
  axios.isAxiosError(error) &&
  [404, 405, 501].includes(error.response?.status ?? 0);

/**
 * Returns the parent_cells for a new transfer: the node's two newest
 * committed transaction IDs. Returns [] only when the node cannot supply
 * them; such a transfer is still accepted before the PoE activation height
 * and is refused with HTTP 422 after it.
 */
export const fetchQsdmPoEParents = async (): Promise<string[]> => {
  try {
    const parents = await fromParentsEndpoint();
    if (parents.length) {
      return parents;
    }
  } catch (error) {
    if (!isMissingRoute(error)) {
      return [];
    }
  }
  try {
    return await fromReceipts();
  } catch {
    return [];
  }
};

/** True when the node refused a transfer because of its PoE parents. */
export const isQsdmPoEParentsRejection = (error: unknown) => {
  if (!axios.isAxiosError(error) || error.response?.status !== 422) {
    return false;
  }
  const { data } = error.response;
  const body = typeof data === 'string' ? data : JSON.stringify(data ?? {});
  return body.toLowerCase().includes('proof-of-entanglement');
};
