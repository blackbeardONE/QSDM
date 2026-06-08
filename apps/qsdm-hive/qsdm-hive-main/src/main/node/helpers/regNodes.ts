import { registerNodes } from 'vendor/qsdm-chain/taskNode';

import { namespaceInstance } from './Namespace';

export default async (newNodes: any[], taskId: string) => {
  return registerNodes(newNodes, taskId, namespaceInstance);
};
