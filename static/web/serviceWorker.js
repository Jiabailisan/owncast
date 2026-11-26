/* eslint-disable no-restricted-globals */
self.addEventListener('activate', event => {
  console.log('Owncast service worker activated', event);
});

self.addEventListener('install', event => {
  console.log('installing Owncast service worker...', event);
});

self.addEventListener('push', event => {
  let data = {};
  try {
    data = JSON.parse(event.data?.text() || '{}');
  } catch (err) {
    console.warn('Invalid push notification payload', err);
  }
  const { title, body, icon, tag } = data;
  const options = {
    title: title || 'Live!',
    body: body || 'This live stream has started.',
    icon: icon || '/logo/external',
    tag,
  };

  event.waitUntil(self.registration.showNotification(options.title, options));
});

self.addEventListener('notificationclick', event => {
  event.notification?.close();
  event.waitUntil(clients.openWindow('/'));
});
