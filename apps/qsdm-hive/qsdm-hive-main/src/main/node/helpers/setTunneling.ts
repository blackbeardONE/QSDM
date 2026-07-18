interface TunnelErrorEmitter {
  on(event: 'error', listener: (error: Error) => void): void;
}

const startLocalTunnel = async (): Promise<{
  success: boolean;
  result?: string;
  error?: string;
  tunnel?: TunnelErrorEmitter;
}> => {
  return {
    success: false,
    error:
      'Third-party localtunnel exposure is disabled. QSDM Core remains available through the configured home or canonical gateway.',
  };
};
export default startLocalTunnel;
