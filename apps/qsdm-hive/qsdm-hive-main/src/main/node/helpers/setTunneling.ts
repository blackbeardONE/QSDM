const localtunnel: any = require('localtunnel');

const startLocalTunnel = async (): Promise<{
  success: boolean;
  result?: string;
  error?: string;
  tunnel?: any;
}> => {
  try {
    const tunnel = await localtunnel({ port: 30017 });
    return {
      success: true,
      result: tunnel.url,
      tunnel,
    };
  } catch (error: any) {
    return {
      success: false,
      error: error.message,
    };
  }
};
export default startLocalTunnel;
