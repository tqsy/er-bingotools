export namespace app {
	
	export class resolveResult {
	    kind: string;
	    title: string;
	    room: string;
	
	    static createFrom(source: any = {}) {
	        return new resolveResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.title = source["title"];
	        this.room = source["room"];
	    }
	}

}

