// Type declarations for the QSDM JavaScript SDK (rebrand of qsdmplus.d.ts).
//
// The QSDM platform was historically shipped under the transitional name "QSDM+".
// This module re-exports the same types with QSDMClient as the preferred symbol
// name; QSDMPlusClient remains available as a legacy alias.
//
// Native coin: Cell (CELL), 8 decimals, smallest unit "dust".

export {
    NodeStatus,
    HealthStatus,
    ClientOptions,
    ApiError,
    isNotFound,
    isUnauthorized,
    QSDMPlusClient,
} from './qsdmplus';

import { QSDMPlusClient } from './qsdmplus';

// Preferred name. Behaviour and wire protocol are identical to QSDMPlusClient.
export class QSDMClient extends QSDMPlusClient {}

export default QSDMClient;
