export const formatUrl = (url: string) => {
  const trimmedUrl = url.trim();
  if (!trimmedUrl) {
    return '';
  }

  const formattedUrl = /^https?:\/\//i.test(trimmedUrl)
    ? trimmedUrl
    : `https://${trimmedUrl}`;
  return formattedUrl;
};

export const isValidUrl = (url: string) => {
  try {
    new URL(url);
  } catch (_) {
    return false;
  }
  return true;
};
