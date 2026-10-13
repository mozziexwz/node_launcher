export namespace main {
	
	export class LinkDTO {
	    name: string;
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new LinkDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.url = source["url"];
	    }
	}
	export class ComponentDTO {
	    name: string;
	    version: string;
	    copyright: string;
	    license: string;
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new ComponentDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.version = source["version"];
	        this.copyright = source["copyright"];
	        this.license = source["license"];
	        this.url = source["url"];
	    }
	}
	export class AboutDTO {
	    version: string;
	    copyright: string;
	    license: string;
	    components: ComponentDTO[];
	    licenseTexts: LinkDTO[];
	
	    static createFrom(source: any = {}) {
	        return new AboutDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.copyright = source["copyright"];
	        this.license = source["license"];
	        this.components = this.convertValues(source["components"], ComponentDTO);
	        this.licenseTexts = this.convertValues(source["licenseTexts"], LinkDTO);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AddonDTO {
	    id: string;
	    name: string;
	    note: string;
	    processes: string[];
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AddonDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.note = source["note"];
	        this.processes = source["processes"];
	        this.enabled = source["enabled"];
	    }
	}
	export class ResultDTO {
	    text: string;
	    detail: string;
	    ok: boolean;
	    averageMs: number;
	
	    static createFrom(source: any = {}) {
	        return new ResultDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.detail = source["detail"];
	        this.ok = source["ok"];
	        this.averageMs = source["averageMs"];
	    }
	}
	export class CheckDTO {
	    exitIP: string;
	    exitIPError: string;
	    entranceTarget: string;
	    entrance: ResultDTO;
	    gameTarget: string;
	    game: ResultDTO;
	
	    static createFrom(source: any = {}) {
	        return new CheckDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.exitIP = source["exitIP"];
	        this.exitIPError = source["exitIPError"];
	        this.entranceTarget = source["entranceTarget"];
	        this.entrance = this.convertValues(source["entrance"], ResultDTO);
	        this.gameTarget = source["gameTarget"];
	        this.game = this.convertValues(source["game"], ResultDTO);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class ConfigDTO {
	    name: string;
	    profile: string;
	    entrance: string;
	    error: string;
	    current: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ConfigDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.profile = source["profile"];
	        this.entrance = source["entrance"];
	        this.error = source["error"];
	        this.current = source["current"];
	    }
	}
	export class ConflictDTO {
	    message: string;
	    blocking: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ConflictDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.message = source["message"];
	        this.blocking = source["blocking"];
	    }
	}
	export class GroupDTO {
	    name: string;
	    processes: string[];
	
	    static createFrom(source: any = {}) {
	        return new GroupDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.processes = source["processes"];
	    }
	}
	export class ImportResultDTO {
	    name: string;
	    exists: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ImportResultDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.exists = source["exists"];
	    }
	}
	export class LegacyResultDTO {
	    imported: string[];
	    skipped: string[];
	    current: string;
	
	    static createFrom(source: any = {}) {
	        return new LegacyResultDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.imported = source["imported"];
	        this.skipped = source["skipped"];
	        this.current = source["current"];
	    }
	}
	
	export class LogLineDTO {
	    seq: number;
	    time: string;
	    tag: string;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new LogLineDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.seq = source["seq"];
	        this.time = source["time"];
	        this.tag = source["tag"];
	        this.text = source["text"];
	    }
	}
	export class StatusDTO {
	    state: string;
	    config: string;
	    sinceUnix: number;
	    accepted: number;
	    active: number;
	    lastError: string;
	    addons: Record<string, boolean>;
	
	    static createFrom(source: any = {}) {
	        return new StatusDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.config = source["config"];
	        this.sinceUnix = source["sinceUnix"];
	        this.accepted = source["accepted"];
	        this.active = source["active"];
	        this.lastError = source["lastError"];
	        this.addons = source["addons"];
	    }
	}
	export class OverviewDTO {
	    status: StatusDTO;
	    configs: ConfigDTO[];
	    addons: AddonDTO[];
	    gameGroups: GroupDTO[];
	    probeAddress: string;
	    probePort: number;
	    elevated: boolean;
	    conflicts: ConflictDTO[];
	    lockError: string;
	    settingsError: string;
	    dataDir: string;
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new OverviewDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = this.convertValues(source["status"], StatusDTO);
	        this.configs = this.convertValues(source["configs"], ConfigDTO);
	        this.addons = this.convertValues(source["addons"], AddonDTO);
	        this.gameGroups = this.convertValues(source["gameGroups"], GroupDTO);
	        this.probeAddress = source["probeAddress"];
	        this.probePort = source["probePort"];
	        this.elevated = source["elevated"];
	        this.conflicts = this.convertValues(source["conflicts"], ConflictDTO);
	        this.lockError = source["lockError"];
	        this.settingsError = source["settingsError"];
	        this.dataDir = source["dataDir"];
	        this.version = source["version"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	

}

