export namespace main {
	
	export class ConfigInfo {
	    path: string;
	    profile: string;
	    entrance: string;
	
	    static createFrom(source: any = {}) {
	        return new ConfigInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.profile = source["profile"];
	        this.entrance = source["entrance"];
	    }
	}
	export class Status {
	    elevated: boolean;
	    accelerating: boolean;
	    simulated: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.elevated = source["elevated"];
	        this.accelerating = source["accelerating"];
	        this.simulated = source["simulated"];
	    }
	}

}

