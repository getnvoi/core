// ds/site's and ds/menu's Stimulus controllers, registered under their names (ds--site, ds--menu), so the header
// behaves as the site's: ds--site toggles the theme, ds--menu opens the Product and Menu panels.
import { Application } from "@hotwired/stimulus";
import Site from "../../design/ds/site/controller.js";
import Menu from "../../design/ds/menu/controller.js";

const application = Application.start();
application.register("ds--site", Site);
application.register("ds--menu", Menu);
