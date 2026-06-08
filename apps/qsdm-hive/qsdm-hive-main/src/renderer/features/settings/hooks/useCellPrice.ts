import { useQuery } from 'react-query';

import { fetchCellPrice } from 'renderer/services/api/utils';

export function useCellPrice() {
  return useQuery({
    queryKey: ['cellPrice'],
    queryFn: fetchCellPrice,
    // Refresh every 1 hour
    refetchInterval: 60 * 60 * 1000,
    // Keep data fresh for 1 hour
    staleTime: 60 * 60 * 1000,
  });
}
