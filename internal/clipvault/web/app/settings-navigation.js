import { app } from './app-context.js';

export function registerSettingsNavigation() {
  app.navigateToSettingsSection = function navigateToSettingsSection(sectionID, updateURL) {
    const section = document.getElementById(sectionID);
    const navigation = document.getElementById('snav-' + sectionID.replace(/^s-/, ''));
    if (!section || !navigation) return false;
    document.querySelectorAll('.snav-item').forEach(element => {
      const current = element === navigation;
      element.classList.toggle('active', current);
      if (current) element.setAttribute('aria-current', 'location');
      else element.removeAttribute('aria-current');
    });
    section.scrollIntoView({ behavior: app.scrollBehavior(), block: 'start' });
    if (updateURL && window.location.hash !== '#' + sectionID) {
      history.pushState(null, '', '#' + sectionID);
    }
    return true;
  };

  window.addEventListener('popstate', () => {
    const sectionID = window.location.hash.slice(1) || 's-theme';
    app.navigateToSettingsSection(sectionID, false);
  });
}
