// Our modals must render above the mobile chat modal: antd's default modal
// z-index is 1000, so a modal opened from inside the chat modal (Change name,
// Authenticate) was hidden behind it on phones. Keep one value for every modal
// we render so the ordering is explicit and cannot drift per call site.
export const MODAL_Z_INDEX = 1100;
