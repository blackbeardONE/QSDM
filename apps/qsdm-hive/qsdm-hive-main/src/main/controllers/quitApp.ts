import { Event } from 'electron';

import { app } from '../app';

export const quitApp = async (_: Event) => {
  app.isQuitting = true;
  app.quit();
};
