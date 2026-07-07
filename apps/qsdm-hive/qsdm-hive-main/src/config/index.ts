import { Endpoints } from './endpoints';
import * as faucet from './faucet';
import node from './node';
import * as qsdm from './qsdm';
import { SystemDbKeys } from './systemDbKeys';

export default {
  node,
  endpoints: Endpoints,
  faucet,
  qsdm,
  SystemDbKeys,
};
